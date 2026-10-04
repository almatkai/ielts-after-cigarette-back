-- History order matches the date displayed in Progress. Cursor paging bounds
-- both metadata lookups and response size without removing older attempts.
CREATE INDEX attempts_history_idx
    ON attempts (user_id, (COALESCE(submitted_at, started_at)) DESC, id DESC);
