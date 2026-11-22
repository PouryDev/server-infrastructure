package payment

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func manual() Refund {
	return Refund{ID: uuid.New(), Amount: money.Must(1000, money.IRR), Status: RefundPending, Method: RefundManualCardToCard}
}

func complete(agent string) CompleteManualInput {
	return CompleteManualInput{Agent: agent, TrackingNumber: "123456789", PaidAt: time.Now(), DestinationCardMasked: "603799******1234", Note: "paid"}
}

func wantKind(t *testing.T, err error, k apperr.Kind, what string) {
	t.Helper()
	if apperr.KindOf(err) != k {
		t.Fatalf("%s: got %v, want kind %v", what, err, k)
	}
}

func TestManualLifecycle(t *testing.T) {
	now := time.Now()
	r := manual()
	if ch, err := r.Claim("ann", now); !ch || err != nil || r.Status != RefundProcessing || r.ClaimedBy != "ann" || r.ClaimedAt == nil {
		t.Fatalf("claim: %v %v %+v", ch, err, r)
	}
	if ch, err := r.Claim("ann", now); ch || err != nil {
		t.Fatalf("claiming twice as the same agent is a no-op: %v %v", ch, err)
	}
	_, err := r.Claim("bob", now)
	wantKind(t, err, apperr.KindConflict, "second agent")

	_, err = r.Release("bob", false)
	wantKind(t, err, apperr.KindConflict, "release by another agent")
	if ch, err := r.Release("ann", false); !ch || err != nil || r.Status != RefundPending || r.ClaimedBy != "" || r.ClaimedAt != nil {
		t.Fatalf("release: %v %v %+v", ch, err, r)
	}
	if _, err := r.Claim("bob", now); err != nil {
		t.Fatal(err)
	}
	if ch, err := r.Release("boss", true); !ch || err != nil {
		t.Fatalf("a supervisor can force a release: %v %v", ch, err)
	}

	_, err = r.CompleteManual(complete("ann"), now)
	wantKind(t, err, apperr.KindConflict, "complete without claim")
	if _, err := r.Claim("ann", now); err != nil {
		t.Fatal(err)
	}
	_, err = r.CompleteManual(complete("bob"), now)
	wantKind(t, err, apperr.KindConflict, "complete by someone else")
	if ch, err := r.CompleteManual(complete("ann"), now); !ch || err != nil || r.Status != RefundDone || r.TrackingNumber != "123456789" || r.PaidAt == nil {
		t.Fatalf("complete: %v %v %+v", ch, err, r)
	}
	if ch, err := r.CompleteManual(complete("ann"), now); ch || err != nil {
		t.Fatalf("retry of complete is a no-op: %v %v", ch, err)
	}
	other := complete("ann")
	other.TrackingNumber = "987654321"
	_, err = r.CompleteManual(other, now)
	wantKind(t, err, apperr.KindConflict, "complete again with another tracking number")
	_, err = r.Cancel("ann", "x", false)
	wantKind(t, err, apperr.KindConflict, "cancel a done refund")
}

func TestFailAndCancel(t *testing.T) {
	r := manual()
	_, err := r.Fail("ann", "bank down", false)
	wantKind(t, err, apperr.KindConflict, "fail needs a claim")
	_, err = r.Cancel("ann", "", false)
	wantKind(t, err, apperr.KindInvalid, "a reason is required")
	if ch, err := r.Cancel("ann", "customer withdrew", false); !ch || err != nil || r.Status != RefundCancelled || r.Failure == "" {
		t.Fatalf("cancel pending: %v %v %+v", ch, err, r)
	}
	if ch, err := r.Cancel("ann", "customer withdrew", false); ch || err != nil {
		t.Fatalf("cancel twice is a no-op: %v %v", ch, err)
	}

	r = manual()
	_, _ = r.Claim("ann", time.Now())
	_, err = r.Fail("bob", "no", false)
	wantKind(t, err, apperr.KindConflict, "fail by another agent")
	if ch, err := r.Fail("ann", "wrong iban", false); !ch || err != nil || r.Status != RefundFailed {
		t.Fatalf("fail: %v %v %+v", ch, err, r)
	}
	if r.Status.Reserved() {
		t.Fatal("a failed refund must not keep its reservation")
	}
}

func TestGatewayRefundsAreNotManual(t *testing.T) {
	r := manual()
	r.Method = RefundViaGateway
	_, err := r.Claim("ann", time.Now())
	wantKind(t, err, apperr.KindConflict, "claim of a gateway refund")
}

func TestCardAndTrackingValidation(t *testing.T) {
	for in, want := range map[string]string{
		"603799******1234":      "603799******1234",
		"6037 99** **** 1234":   "603799******1234",
		"************1234":      "************1234",
		"۶۰۳۷۹۹******۱۲۳۴":      "603799******1234",
		"603799••••••1234":      "603799******1234",
		"6037991234567890":      "",
		"603799******12345":     "",
		"60379912******1234":    "",
		"6037******1234":        "",
		"603799*****1234":       "",
		"":                      "",
		"603799******1234 5678": "",
	} {
		got, err := ValidateMaskedCard(in)
		if (want == "") != (err != nil) || got != want {
			t.Errorf("ValidateMaskedCard(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for in, ok := range map[string]bool{
		"123456789": true, "AB-12/34.5": true, "۱۲۳۴۵۶": true, "123": false, "": false, "   ": false,
		"6037997012345675": false, // Luhn-valid 16 digits: a card number in the wrong field
		"1234567890123456": true,  // 16 digits but not a card number
		"has space1234":    false, strings.Repeat("a", 50): false,
	} {
		if _, err := ValidateTrackingNumber(in); (err == nil) != ok {
			t.Errorf("ValidateTrackingNumber(%q): err=%v, want ok=%v", in, err, ok)
		}
	}
	if !containsCardNumber("paid to 6037-9970-1234-5675 ok") || !containsCardNumber("۶۰۳۷۹۹۷۰۱۲۳۴۵۶۷۵") {
		t.Error("a real card number in a note must be detected")
	}
	if containsCardNumber("order 12345678 on 2026-11-09, tracking 99887766") {
		t.Error("ordinary numbers must not be mistaken for a card number")
	}
	r := manual()
	_, _ = r.Claim("ann", time.Now())
	in := complete("ann")
	in.Note = "card 6037997012345675"
	_, err := r.CompleteManual(in, time.Now())
	wantKind(t, err, apperr.KindInvalid, "note with a card number")
	in = complete("ann")
	in.PaidAt = time.Now().Add(time.Hour)
	_, err = r.CompleteManual(in, time.Now())
	wantKind(t, err, apperr.KindInvalid, "paid_at in the future")
}
