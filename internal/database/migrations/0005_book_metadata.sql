-- Keep user corrections separate from import-time metadata repairs.
ALTER TABLE books ADD COLUMN metadata_edited INTEGER NOT NULL DEFAULT 0;
