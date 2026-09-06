-- migrate:up

-- `buyer_not_found` recorded a rejection that only the two-table identity split
-- could produce: an authenticated principal whose facts were attributed to a
-- user row that did not exist.
-- [ADR-0034](../../docs/adr/0034-make-the-principal-the-sole-identity.md)
-- removed that split, and no path has produced the code since. The application
-- kept decoding it only so that a stored rejection still replays verbatim,
-- which is the guarantee that outlives the code that wrote it.
--
-- Prove that no retained result carries the code, then forbid it. With the
-- history clean, the decode has nothing left to decode and the application can
-- drop it; with the constraint in place, no alternate writer can reintroduce a
-- value the application would no longer understand.
--
-- Operation results are append-only, so a retained row cannot be rewritten
-- here. Report it and stop: an operator whose history still carries the code
-- must keep a binary that decodes it until those results are dispositioned
-- under review.

DO $$
DECLARE
  retained_count bigint;
BEGIN
  SELECT count(*) INTO retained_count
  FROM bond_exchange.operation_results
  WHERE safe_error_code = 'buyer_not_found';

  IF retained_count > 0 THEN
    RAISE EXCEPTION
      'cannot retire buyer_not_found: % operation result(s) still record it',
      retained_count
      USING HINT =
        'Operation results are append-only, and an exact retry replays a '
        'stored rejection verbatim. Keep an application version that decodes '
        'this code until those results are dispositioned as new facts in a '
        'reviewed corrective forward migration.';
  END IF;
END;
$$;

ALTER TABLE bond_exchange.operation_results
  ADD CONSTRAINT operation_results_buyer_not_found_retired
  CHECK (safe_error_code <> 'buyer_not_found');

-- migrate:down

ALTER TABLE bond_exchange.operation_results
  DROP CONSTRAINT operation_results_buyer_not_found_retired;
