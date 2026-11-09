-- +goose Up
-- Every journal must balance at COMMIT: sum(debits) = sum(credits) per currency.
-- DEFERRABLE INITIALLY DEFERRED is required. An immediate trigger would reject
-- the first of two lines before the matching credit exists. Application code
-- (ledger.Post) validates first. This trigger is the last line of defence
-- against raw SQL and future call sites that skip Post.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assert_journal_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    jid uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        jid := OLD.journal_id;
    ELSE
        jid := NEW.journal_id;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM postings
        WHERE journal_id = jid
        GROUP BY currency
        HAVING SUM(CASE WHEN direction = 'debit' THEN amount_minor ELSE 0 END)
            <> SUM(CASE WHEN direction = 'credit' THEN amount_minor ELSE 0 END)
    ) THEN
        RAISE EXCEPTION
            'postings_journal_balanced: journal % debits do not equal credits',
            jid
            USING ERRCODE = '23514';
    END IF;

    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER postings_journal_balanced
    AFTER INSERT OR UPDATE OR DELETE ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE PROCEDURE assert_journal_balanced();

-- +goose Down
DROP TRIGGER IF EXISTS postings_journal_balanced ON postings;
DROP FUNCTION IF EXISTS assert_journal_balanced();
