-- Add a mode column to revisions so each turn remembers which writing persona
-- produced it. Existing rows (created before this migration) default to 'academic'.
ALTER TABLE revisions ADD COLUMN mode TEXT NOT NULL DEFAULT 'academic';
