-- Enforce the run state machine in the database as well as in Go
-- (jobs.CanTransition). Every status change that is not listed here is
-- rejected, whichever code path issues it.
--
--   queued  -> running | cancelled
--   running -> succeeded | failed | cancelled | interrupted
--   running -> queued    only for read-only runs with attempts left
--
-- Terminal states (succeeded, failed, cancelled, interrupted) have no
-- outgoing transitions. Resubmitting creates a new row.
CREATE FUNCTION runs_guard_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    IF (OLD.status = 'queued' AND NEW.status IN ('running', 'cancelled'))
       OR (OLD.status = 'running' AND NEW.status IN ('succeeded', 'failed', 'cancelled', 'interrupted'))
       OR (OLD.status = 'running' AND NEW.status = 'queued'
           AND NOT OLD.mutating AND OLD.attempt < OLD.max_attempts) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invalid run status transition % -> % (run %)', OLD.status, NEW.status, OLD.id
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER runs_guard_transition
    BEFORE UPDATE OF status ON runs
    FOR EACH ROW EXECUTE FUNCTION runs_guard_transition();
