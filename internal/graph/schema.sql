CREATE TABLE nodes (
	id         TEXT PRIMARY KEY,
	kind       TEXT NOT NULL,
	name       TEXT NOT NULL,
	package    TEXT NOT NULL DEFAULT '',
	detail     TEXT NOT NULL DEFAULT '',
	file       TEXT NOT NULL DEFAULT '',
	start_line INTEGER NOT NULL DEFAULT 0,
	start_col  INTEGER NOT NULL DEFAULT 0,
	end_line   INTEGER NOT NULL DEFAULT 0,
	end_col    INTEGER NOT NULL DEFAULT 0,
	attrs      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX nodes_kind ON nodes (kind);
CREATE INDEX nodes_package ON nodes (package);

CREATE TABLE edges (
	id         INTEGER PRIMARY KEY,
	src        TEXT NOT NULL REFERENCES nodes (id),
	dst        TEXT NOT NULL REFERENCES nodes (id),
	kind       TEXT NOT NULL,
	file       TEXT NOT NULL DEFAULT '',
	start_line INTEGER NOT NULL DEFAULT 0,
	start_col  INTEGER NOT NULL DEFAULT 0,
	end_line   INTEGER NOT NULL DEFAULT 0,
	end_col    INTEGER NOT NULL DEFAULT 0,
	attrs      TEXT NOT NULL DEFAULT '',
	UNIQUE (src, dst, kind, file, start_line, start_col)
);

CREATE INDEX edges_src ON edges (src, kind);
CREATE INDEX edges_dst ON edges (dst, kind);

-- Trigram tokens give substring matches: "group" finds "CreateGroup". The
-- ID makes qualified names searchable: "sql.Tx).ExecContext".
CREATE VIRTUAL TABLE nodes_fts USING fts5 (
	name, package, file, detail, id,
	content = 'nodes', content_rowid = 'rowid', tokenize = 'trigram'
);

CREATE TRIGGER nodes_ai AFTER INSERT ON nodes BEGIN
	INSERT INTO nodes_fts (rowid, name, package, file, detail, id)
	VALUES (new.rowid, new.name, new.package, new.file, new.detail, new.id);
END;

CREATE TRIGGER nodes_ad AFTER DELETE ON nodes BEGIN
	INSERT INTO nodes_fts (nodes_fts, rowid, name, package, file, detail, id)
	VALUES ('delete', old.rowid, old.name, old.package, old.file, old.detail, old.id);
END;

CREATE TRIGGER nodes_au AFTER UPDATE ON nodes BEGIN
	INSERT INTO nodes_fts (nodes_fts, rowid, name, package, file, detail, id)
	VALUES ('delete', old.rowid, old.name, old.package, old.file, old.detail, old.id);
	INSERT INTO nodes_fts (rowid, name, package, file, detail, id)
	VALUES (new.rowid, new.name, new.package, new.file, new.detail, new.id);
END;
