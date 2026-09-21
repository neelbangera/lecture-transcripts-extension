PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY
);

CREATE TABLE jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lecture_key TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  job_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT,
  lease_started_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_error_category TEXT,
  last_error_http_status INTEGER,
  remote_content_hash TEXT,
  remote_file_kind TEXT,
  UNIQUE (lecture_key, content_hash),
  CHECK (attempt_count >= 0),
  CHECK (length(content_hash) = 64)
);

CREATE INDEX jobs_ready_idx
  ON jobs (status, next_attempt_at, created_at);

CREATE INDEX jobs_lecture_idx
  ON jobs (lecture_key, created_at);

INSERT INTO schema_migrations(version) VALUES (1);
