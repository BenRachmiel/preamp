package db

const schema = `
CREATE TABLE IF NOT EXISTS artist (
	id   TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE
);
CREATE INDEX IF NOT EXISTS idx_artist_name ON artist(name);

CREATE TABLE IF NOT EXISTS album (
	id         TEXT PRIMARY KEY,
	artist_id  TEXT NOT NULL REFERENCES artist(id),
	name       TEXT NOT NULL,
	year       INTEGER,
	genre      TEXT,
	cover_art  TEXT,  -- path to extracted cover, empty if none
	song_count INTEGER NOT NULL DEFAULT 0,
	duration   INTEGER NOT NULL DEFAULT 0,  -- total seconds
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_album_artist ON album(artist_id);
CREATE INDEX IF NOT EXISTS idx_album_created ON album(created_at);

CREATE TABLE IF NOT EXISTS song (
	id           TEXT PRIMARY KEY,
	album_id     TEXT NOT NULL REFERENCES album(id),
	artist_id    TEXT NOT NULL REFERENCES artist(id),
	title        TEXT NOT NULL,
	track        INTEGER,
	disc         INTEGER,
	year         INTEGER,
	genre        TEXT,
	duration     INTEGER NOT NULL DEFAULT 0,  -- seconds
	size         INTEGER NOT NULL DEFAULT 0,  -- bytes
	suffix       TEXT NOT NULL,               -- mp3, flac, etc.
	bitrate      INTEGER NOT NULL DEFAULT 0,  -- kbps
	content_type TEXT NOT NULL,               -- audio/mpeg, etc.
	path         TEXT NOT NULL UNIQUE,        -- absolute fs path
	created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_song_album ON song(album_id);
CREATE INDEX IF NOT EXISTS idx_song_title ON song(title);
CREATE INDEX IF NOT EXISTS idx_song_genre ON song(genre);

CREATE VIRTUAL TABLE IF NOT EXISTS song_fts USING fts5(
	title,
	artist_name,
	album_name,
	content='',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TABLE IF NOT EXISTS credential (
	id                 TEXT PRIMARY KEY,
	username           TEXT NOT NULL UNIQUE,
	encrypted_password BLOB NOT NULL,
	client_name        TEXT,
	expires_at         TEXT,
	created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE TABLE IF NOT EXISTS star (
	id        TEXT PRIMARY KEY,
	user_id   TEXT NOT NULL,
	item_id   TEXT NOT NULL,
	item_type TEXT NOT NULL,  -- artist, album, song
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
	UNIQUE(user_id, item_id, item_type)
);
CREATE INDEX IF NOT EXISTS idx_star_user_type ON star(user_id, item_type);

CREATE TABLE IF NOT EXISTS play_history (
	id        TEXT PRIMARY KEY,
	song_id   TEXT NOT NULL REFERENCES song(id),
	user_id   TEXT NOT NULL,
	played_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_play_history_played ON play_history(played_at);
CREATE INDEX IF NOT EXISTS idx_play_history_user_song ON play_history(user_id, song_id);

CREATE TABLE IF NOT EXISTS playlist (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL,
	name       TEXT NOT NULL,
	comment    TEXT NOT NULL DEFAULT '',
	public     INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_playlist_user ON playlist(user_id);

CREATE TABLE IF NOT EXISTS playlist_song (
	playlist_id TEXT NOT NULL REFERENCES playlist(id) ON DELETE CASCADE,
	song_id     TEXT NOT NULL REFERENCES song(id),
	position    INTEGER NOT NULL,
	PRIMARY KEY (playlist_id, position)
);

CREATE TABLE IF NOT EXISTS rating (
	user_id TEXT NOT NULL,
	item_id TEXT NOT NULL,
	rating  INTEGER NOT NULL CHECK(rating BETWEEN 1 AND 5),
	PRIMARY KEY(user_id, item_id)
);
`
