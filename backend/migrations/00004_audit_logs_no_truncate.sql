-- +goose Up
-- Row triggers do not fire for TRUNCATE, so a single statement could still
-- empty audit_logs. This statement trigger closes that hole. The function
-- itself is created in 00003.
CREATE TRIGGER audit_logs_no_truncate
    BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT
    EXECUTE PROCEDURE audit_logs_append_only();

-- +goose Down
DROP TRIGGER IF EXISTS audit_logs_no_truncate ON audit_logs;
