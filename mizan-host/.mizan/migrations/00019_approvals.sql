-- +goose Up
-- Two-person approval of year-end closing and profit appropriation: a request by the preparer, executed by a different approver.

CREATE TABLE mz_approvals (
    id            uuid PRIMARY KEY,
    kind          text NOT NULL,
    subject       text NOT NULL,
    payload       jsonb NOT NULL,
    status        text NOT NULL CHECK (status IN ('pending', 'executed', 'rejected')),
    requested_by  text NOT NULL,
    decided_by    text NOT NULL DEFAULT '',
    reason        text NOT NULL DEFAULT '',
    result        jsonb,
    idem_key      text NOT NULL UNIQUE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    decided_at    timestamptz,
    CHECK (status = 'pending' OR decided_by <> '')
);
CREATE UNIQUE INDEX mz_approvals_one_pending ON mz_approvals (kind, subject) WHERE status = 'pending';

-- +goose Down
DROP TABLE mz_approvals;
