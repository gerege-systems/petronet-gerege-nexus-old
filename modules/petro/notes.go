package petro

import (
	"errors"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// maxNoteRunes is the length migration 00017 allows a free-text note.
//
// Checked in the handler as well as by the column's CHECK, because a violated
// constraint comes back as 23514, and several handlers read 23514 as their own
// business rule — a receipt answered "the tank is full" for a note that was
// merely too long. The body limits (2–4 KB) do not settle it: they count bytes
// of the whole payload, and a note of 4 001 ASCII characters still fits.
const maxNoteRunes = 4000

const noteTooLongMessage = "тэмдэглэл 4000 тэмдэгтээс хэтэрч болохгүй"

func noteTooLong(note string) bool { return utf8.RuneCountInString(note) > maxNoteRunes }

// violatedConstraint names the constraint a Postgres error is about, or "".
func violatedConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}
