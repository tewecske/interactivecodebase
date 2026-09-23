// Command gormapp reads and writes through gorm: models named by
// convention and by TableName, chained queries, Raw and Exec.
package main

import (
	"gorm.io/gorm"
)

type User struct {
	ID    int64
	Email string
	Name  string
	Notes []Note
}

type Note struct {
	ID     int64
	UserID int64
	Body   string
}

// AuditEntry lives in a table not named after it.
type AuditEntry struct {
	ID     int64
	Action string
}

func (AuditEntry) TableName() string { return "audit_log" }

type store struct{ db *gorm.DB }

func (s *store) userByEmail(email string) (User, error) {
	var u User
	err := s.db.Where("email = ?", email).First(&u).Error
	return u, err
}

func (s *store) notes(userID int64) ([]Note, error) {
	var notes []Note
	err := s.db.Where("user_id = ?", userID).Order("id").Find(&notes).Error
	return notes, err
}

func (s *store) createNote(n *Note) error { return s.db.Create(n).Error }

func (s *store) rename(id int64, name string) error {
	return s.db.Model(&User{}).Where("id = ?", id).Update("name", name).Error
}

func (s *store) deleteNote(id int64) error { return s.db.Delete(&Note{}, id).Error }

func (s *store) audit(action string) error {
	return s.db.Create(&AuditEntry{Action: action}).Error
}

func (s *store) countNotes() (int64, error) {
	var n int64
	err := s.db.Table("notes").Count(&n).Error
	return n, err
}

func (s *store) emails() ([]string, error) {
	var out []string
	err := s.db.Raw("SELECT email FROM users ORDER BY email").Scan(&out).Error
	return out, err
}

func (s *store) purge() error {
	return s.db.Exec("DELETE FROM audit_log WHERE id < ?", 100).Error
}

func main() {
	s := &store{}
	_, _ = s.userByEmail("a")
	_, _ = s.notes(1)
	_ = s.createNote(&Note{})
	_ = s.rename(1, "b")
	_ = s.deleteNote(1)
	_ = s.audit("x")
	_, _ = s.countNotes()
	_, _ = s.emails()
	_ = s.purge()
}
