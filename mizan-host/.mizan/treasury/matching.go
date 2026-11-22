package treasury

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

const maxImportRows = 20000

// ImportStatement stores the lines of a bank statement of one account. Importing the same lines again, or an
// overlapping statement, adds only the new ones.
func (s *Service) ImportStatement(ctx context.Context, account uuid.UUID, rows []StatementRow, actor string) (ImportResult, error) {
	res := ImportResult{Read: len(rows)}
	if len(rows) == 0 || len(rows) > maxImportRows {
		return res, apperr.Invalid("a statement has between 1 and 20000 lines")
	}
	for i, r := range rows {
		if r.Date.IsZero() || r.Amount == 0 || len(r.Description) > 1000 || len(r.Reference) > 200 {
			return res, apperr.Invalid("invalid statement line " + itoa(i+1))
		}
		rows[i].Date = DateOnly(r.Date)
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.BankAccount(ctx, account); err != nil {
			return err
		}
		fps := Fingerprints(account, rows)
		now := s.clock()
		lines := make([]Line, len(rows))
		for i, r := range rows {
			lines[i] = Line{ID: uuid.New(), AccountID: account, Date: r.Date, Amount: r.Amount, Description: strings.TrimSpace(r.Description),
				Reference: strings.TrimSpace(r.Reference), Balance: r.Balance, Fingerprint: fps[i], Status: LineUnmatched, ImportedBy: actor, CreatedAt: now}
		}
		n, err := s.repo.InsertLines(ctx, lines)
		if err != nil {
			return err
		}
		res.Inserted, res.Skipped = n, len(rows)-n
		return s.note(ctx, actor, "treasury.statement_imported", "bank_account", account, "",
			map[string]any{"read": res.Read, "inserted": res.Inserted, "skipped": res.Skipped})
	})
	return res, err
}

func itoa(n int) string { return strconv.Itoa(n) }

// Line returns a statement line.
func (s *Service) Line(ctx context.Context, id uuid.UUID) (Line, error) {
	return s.repo.Line(ctx, id, false)
}

// ListLines lists statement lines (filter[status]=unmatched, filter[bank_account_id]=..., oldest first by default).
func (s *Service) ListLines(ctx context.Context, p query.Params) (query.Page[Line], error) {
	return s.repo.ListLines(ctx, p)
}

// Suggest lists what a statement line may belong to: cheques and gateway payouts of the same account and exactly the
// same amount. A candidate is High when the line's text also names it (the cheque's Sayad id, the payout's reference).
func (s *Service) Suggest(ctx context.Context, lineID uuid.UUID) ([]Candidate, error) {
	l, err := s.repo.Line(ctx, lineID, false)
	if err != nil {
		return nil, err
	}
	return s.suggest(ctx, l)
}

func (s *Service) suggest(ctx context.Context, l Line) ([]Candidate, error) {
	if l.Status != LineUnmatched {
		return nil, nil
	}
	text := strings.ToLower(normalizeDigits(l.Description + " " + l.Reference))
	abs := l.Amount
	if abs < 0 {
		abs = -abs
	}
	amount, err := money.New(abs, s.cfg.Currency)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	if l.Amount > 0 {
		cs, err := s.repo.ChequeCandidates(ctx, l.AccountID, Receivable, []ChequeStatus{Deposited}, amount)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			out = append(out, chequeCandidate(c, text))
		}
		sts, err := s.repo.UnlinkedSettlements(ctx, l.AccountID, amount)
		if err != nil {
			return nil, err
		}
		for _, st := range sts {
			high := len(st.Ref) >= 6 && strings.Contains(text, strings.ToLower(st.Ref))
			out = append(out, Candidate{Kind: MatchSettlement, ID: st.ID, High: high, Reason: "gateway payout " + st.Gateway + " " + st.Ref + " with the same net amount"})
		}
		return out, nil
	}
	cs, err := s.repo.ChequeCandidates(ctx, l.AccountID, Payable, []ChequeStatus{Issued, Bounced}, amount)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		out = append(out, chequeCandidate(c, text))
	}
	return out, nil
}

func chequeCandidate(c Cheque, text string) Candidate {
	high := c.SayadID != "" && strings.Contains(text, c.SayadID)
	reason := "cheque " + c.Number + " with the same amount"
	if high {
		reason = "cheque " + c.Number + ": its Sayad id is in the line and the amount is equal"
	}
	return Candidate{Kind: MatchCheque, ID: c.ID, High: high, Reason: reason}
}

// MatchInput matches a statement line.
type MatchInput struct {
	LineID uuid.UUID
	Kind   MatchKind
	// ItemID is the cheque or the gateway payout; unused for a fee or an ignored line.
	ItemID uuid.UUID
	// Note is required to ignore a line (why nothing is booked).
	Note  string
	Actor string
}

// MatchLine settles a statement line against what it is:
//   - cheque: the cheque is cleared on the line's date (a received one must have been deposited into this account,
//     an issued one drawn on it) and the amounts must be equal;
//   - settlement: a gateway payout already booked is confirmed (net amount equal, same account);
//   - fee: a bank charge, booked as an expense against the account;
//   - ignored: nothing is booked, the note says why.
//
// Matching the same line to the same item again is a no-op.
func (s *Service) MatchLine(ctx context.Context, in MatchInput) (Line, error) {
	var out Line
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		l, err := s.repo.Line(ctx, in.LineID, true)
		if err != nil {
			return err
		}
		out = l
		if l.Status != LineUnmatched {
			if l.MatchKind == in.Kind && (in.Kind == MatchFee || in.Kind == MatchIgnored || l.MatchID == in.ItemID.String()) {
				return nil
			}
			return apperr.New(apperr.KindConflict, "line_already_matched", "the statement line is already "+string(l.Status))
		}
		abs := l.Amount
		if abs < 0 {
			abs = -abs
		}
		var jid *uuid.UUID
		matchID := ""
		switch in.Kind {
		case MatchCheque:
			c, err := s.repo.Cheque(ctx, in.ItemID, true)
			if err != nil {
				return err
			}
			if c.Amount.Amount() != abs || (c.Direction == Receivable) != (l.Amount > 0) {
				return apperr.New(apperr.KindConflict, "line_cheque_mismatch", "the line's amount or direction does not fit the cheque")
			}
			if c.BankAccountID == nil || *c.BankAccountID != l.AccountID {
				return apperr.New(apperr.KindConflict, "line_cheque_mismatch", "the cheque belongs to another bank account")
			}
			cleared, err := s.ClearCheque(ctx, c.ID, l.Date, in.Actor)
			if err != nil {
				return err
			}
			jid, matchID = cleared.FinalJournal, c.ID.String()
		case MatchSettlement:
			st, err := s.repo.Settlement(ctx, in.ItemID, true)
			if err != nil {
				return err
			}
			if l.Amount <= 0 || st.Net.Amount() != abs || st.AccountID != l.AccountID {
				return apperr.New(apperr.KindConflict, "line_settlement_mismatch", "the line's amount or account does not fit the payout")
			}
			if st.LineID != nil {
				return apperr.New(apperr.KindConflict, "settlement_confirmed", "the payout is already confirmed by another line")
			}
			if err := s.repo.LinkSettlement(ctx, st.ID, l.ID); err != nil {
				return err
			}
			j := st.JournalID
			jid, matchID = &j, st.ID.String()
		case MatchFee:
			if l.Amount >= 0 {
				return apperr.New(apperr.KindConflict, "line_not_a_charge", "only money going out can be a bank charge")
			}
			acc, err := s.repo.BankAccount(ctx, l.AccountID)
			if err != nil {
				return err
			}
			fee, err := money.New(abs, acc.Currency)
			if err != nil {
				return err
			}
			j, err := s.books.Post(ctx, books.Entry{Key: "bankline:" + l.ID.String() + ":fee", Date: l.Date, Description: "Bank charge: " + l.Description,
				RefType: "bank_statement_line", RefID: l.ID.String(), Actor: in.Actor,
				Lines: []books.Line{books.Dr(s.cfg.Accounts.BankFees, fee), books.Cr(acc.LedgerCode, fee)}})
			if err != nil {
				return err
			}
			jid = &j
		case MatchIgnored:
			if strings.TrimSpace(in.Note) == "" {
				return apperr.InvalidField("note", "say why the line is ignored")
			}
		default:
			return apperr.InvalidField("kind", "cheque, settlement, fee or ignored")
		}
		now := s.clock()
		l.Status, l.MatchKind, l.MatchID, l.MatchedBy, l.MatchedAt, l.JournalID = LineMatched, in.Kind, matchID, in.Actor, &now, jid
		if in.Kind == MatchIgnored {
			l.Status = LineIgnored
		}
		if err := s.repo.UpdateLine(ctx, &l); err != nil {
			return err
		}
		out = l
		return s.note(ctx, in.Actor, "treasury.line_"+string(l.Status), "bank_statement_line", l.ID, "treasury.line_matched",
			map[string]any{"line_id": l.ID, "kind": in.Kind, "item_id": matchID, "amount": l.Amount, "note": in.Note})
	})
	return out, err
}

// AutoResult counts what AutoMatch did.
type AutoResult struct{ Checked, Matched, Ambiguous, Failed int }

// AutoMatch matches the open lines of an account that name their item (a High candidate) when exactly one such
// candidate exists. Everything else is left for a person: the amount alone is never enough to move money.
func (s *Service) AutoMatch(ctx context.Context, account uuid.UUID, limit int, actor string) (AutoResult, error) {
	var res AutoResult
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	lines, err := s.repo.UnmatchedLines(ctx, account, limit)
	if err != nil {
		return res, err
	}
	for _, l := range lines {
		res.Checked++
		cands, err := s.suggest(ctx, l)
		if err != nil {
			return res, err
		}
		var high []Candidate
		for _, c := range cands {
			if c.High {
				high = append(high, c)
			}
		}
		if len(high) != 1 {
			if len(high) > 1 {
				res.Ambiguous++
			}
			continue
		}
		if _, err := s.MatchLine(ctx, MatchInput{LineID: l.ID, Kind: high[0].Kind, ItemID: high[0].ID, Actor: actor}); err != nil {
			res.Failed++
			continue
		}
		res.Matched++
	}
	return res, nil
}
