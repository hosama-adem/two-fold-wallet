-- +goose Up
-- journal_entries and postings are history. A mistake is a new reversal
-- journal, never an edit. Row triggers miss TRUNCATE, so that statement
-- has its own trigger, same as audit_logs.
-- The balanced-journal trigger still runs on INSERT. UPDATE and DELETE
-- never reach it: these BEFORE triggers raise first.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger_append_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME
        USING ERRCODE = 'integrity_constraint_violation';
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER journal_entries_no_update
    BEFORE UPDATE ON journal_entries
    FOR EACH ROW
    EXECUTE PROCEDURE ledger_append_only();

CREATE TRIGGER journal_entries_no_delete
    BEFORE DELETE ON journal_entries
    FOR EACH ROW
    EXECUTE PROCEDURE ledger_append_only();

CREATE TRIGGER journal_entries_no_truncate
    BEFORE TRUNCATE ON journal_entries
    FOR EACH STATEMENT
    EXECUTE PROCEDURE ledger_append_only();

CREATE TRIGGER postings_no_update
    BEFORE UPDATE ON postings
    FOR EACH ROW
    EXECUTE PROCEDURE ledger_append_only();

CREATE TRIGGER postings_no_delete
    BEFORE DELETE ON postings
    FOR EACH ROW
    EXECUTE PROCEDURE ledger_append_only();

CREATE TRIGGER postings_no_truncate
    BEFORE TRUNCATE ON postings
    FOR EACH STATEMENT
    EXECUTE PROCEDURE ledger_append_only();

-- +goose Down
DROP TRIGGER IF EXISTS postings_no_truncate ON postings;
DROP TRIGGER IF EXISTS postings_no_delete ON postings;
DROP TRIGGER IF EXISTS postings_no_update ON postings;
DROP TRIGGER IF EXISTS journal_entries_no_truncate ON journal_entries;
DROP TRIGGER IF EXISTS journal_entries_no_delete ON journal_entries;
DROP TRIGGER IF EXISTS journal_entries_no_update ON journal_entries;
DROP FUNCTION IF EXISTS ledger_append_only();
