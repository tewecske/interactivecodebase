ALTER TABLE notes
    ADD CONSTRAINT notes_owner_id_fk
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE CASCADE;

ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_note_id_fk
    FOREIGN KEY (note_id) REFERENCES notes (id) ON DELETE SET NULL;
