package petro

// A submission's header as the caller may read it.
//
// row_count, error_count, warning_count and review_note are stored for the
// whole submission. A province oversight body reads only the lines and the
// findings of its own province (migration 00018), so a company trading in two
// provinces showed the other province's figures through the header — "3
// errors" above the one clean line the body could see. For scope 'aimag' the
// counts are taken from the rows the policies let it read, and the review
// note, written about the whole submission, is withheld. Every other caller
// reads the stored values, unchanged.
//
// Both expect the submissions table aliased `s`. The scope is asked once per
// statement: the scalar subquery becomes an InitPlan.
const submissionCountsSQL = `
       CASE WHEN (SELECT petro_oversight_scope()) = 'aimag'
            THEN (SELECT COUNT(*)::int FROM petro_report_lines l WHERE l.submission_id = s.id)
            ELSE s.row_count END,
       CASE WHEN (SELECT petro_oversight_scope()) = 'aimag'
            THEN (SELECT COUNT(*)::int FROM petro_validation_findings f
                   WHERE f.submission_id = s.id AND f.severity = 'error')
            ELSE s.error_count END,
       CASE WHEN (SELECT petro_oversight_scope()) = 'aimag'
            THEN (SELECT COUNT(*)::int FROM petro_validation_findings f
                   WHERE f.submission_id = s.id AND f.severity = 'warning')
            ELSE s.warning_count END`

const submissionReviewNoteSQL = `
       CASE WHEN (SELECT petro_oversight_scope()) = 'aimag' THEN '' ELSE s.review_note END`
