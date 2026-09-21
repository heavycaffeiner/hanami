package app

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type Note struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type NoteRepository struct{ db *gorm.DB }

func NewNoteRepository(db *gorm.DB) *NoteRepository { return &NoteRepository{db: db} }
func (r *NoteRepository) Create(ctx context.Context, note *Note) error {
	return gorm.G[Note](r.db).Create(ctx, note)
}
func (r *NoteRepository) List(ctx context.Context) ([]Note, error) {
	return gorm.G[Note](r.db).Order("id ASC").Find(ctx)
}

type NoteService struct{ repository *NoteRepository }

func NewNoteService(repository *NoteRepository) *NoteService {
	return &NoteService{repository: repository}
}
func (s *NoteService) Create(ctx context.Context, title, content string) (Note, error) {
	note := Note{Title: title, Content: content}
	if err := s.repository.Create(ctx, &note); err != nil {
		return Note{}, err
	}
	return note, nil
}
func (s *NoteService) List(ctx context.Context) ([]Note, error) { return s.repository.List(ctx) }

type CreateNoteInput struct {
	Body struct {
		Title   string `json:"title" minLength:"1"`
		Content string `json:"content"`
	}
}
type NoteOutput struct{ Body Note }
type ListNotesInput struct{}
type ListNotesOutput struct {
	Body struct {
		Notes []Note `json:"notes"`
	}
}
