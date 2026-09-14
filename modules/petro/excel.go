/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * One spreadsheet, handed out and taken back.
 *
 * A regulator that accepts whatever workbook arrives is a regulator that
 * employs people to retype workbooks. So the template is issued by the system,
 * already carrying the sites, the grades and yesterday's closing figures, and
 * the reader below expects exactly the sheet it issued: fourteen columns, in
 * order, on a sheet with a known name.
 *
 * # Refusing is the feature
 *
 * A file that does not match is refused with the row and the column that did
 * not match, not with "invalid format". The sender then has one thing to fix.
 * Accepting a loose file and guessing at its columns is how a national dataset
 * becomes a set of two hundred dialects — the state Mongolia's fuel reporting
 * is in today.
 *
 * # The identity columns are not for the reader
 *
 * site_kind and site_id travel in the sheet so that a row cannot be matched to
 * a site by its name. Two companies have a station called "Төв"; a fuzzy match
 * on that name is the bug you find six months later in the national total.
 */

package petro

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"
)

// sheetName is the one sheet read and written. Named in Mongolian because the
// person opening it reads Mongolian, and pinned because a renamed sheet is the
// most common way a template stops being the template.
const sheetName = "Тайлан"

// templateHeader is the contract. Order matters; the reader trusts position,
// not the header text, and the header text exists for the human.
var templateHeader = []string{
	"Объектын төрөл", "Объектын ID", "Объектын нэр",
	"Бүтээгдэхүүн", "Нэр",
	"Нээлтийн үлдэгдэл", "Хүлээн авалт", "Борлуулалт", "Шилжүүлэг", "Залруулга",
	"Хаалтын үлдэгдэл", "Үнэ (₮)", "Температур (°C)", "Нягт (кг/м³)", "Тайлбар",
}

// Column positions, so the reader and the writer cannot drift apart.
const (
	colSiteKind = iota
	colSiteID
	colSiteName
	colProduct
	colProductLabel
	colOpening
	colReceipts
	colSales
	colTransfers
	colAdjustments
	colClosing
	colPrice
	colTemperature
	colDensity
	colNote
)

// handleTemplate issues the workbook for one period.
func (m *Module) handleTemplate(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}
	periodID := chi.URLParam(r, "id")
	if !isUUID(periodID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	period, err := m.readPeriod(r.Context(), periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм тайлангийн үе олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the period")
		return
	}

	contexts, order, err := m.loadLineContexts(r.Context(), period.PeriodStart)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not assemble the template")
		return
	}
	labels, err := m.productLabels(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the product dictionary")
		return
	}

	file := excelize.NewFile()
	defer func() { _ = file.Close() }()

	index, err := file.NewSheet(sheetName)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not build the workbook")
		return
	}
	file.SetActiveSheet(index)
	_ = file.DeleteSheet("Sheet1")

	for i, title := range templateHeader {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = file.SetCellValue(sheetName, cell, title)
	}

	row := 2
	for _, key := range order {
		c := contexts[key]
		kind, id, product := splitLineKey(key)
		opening := 0.0
		if c.PrevClosing != nil {
			opening = *c.PrevClosing
		}

		values := []any{kind, id, c.SiteName, product, labels[product], opening,
			nil, nil, nil, nil, nil, nil, nil, nil, ""}
		if c.PrevPrice != nil {
			values[colPrice] = *c.PrevPrice
		}
		for i, value := range values {
			if value == nil {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			_ = file.SetCellValue(sheetName, cell, value)
		}
		row++
	}

	filename := fmt.Sprintf("petronet-%s.xlsx", period.PeriodStart)
	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if err := file.Write(w); err != nil {
		// The status line is already sent; there is nothing left to say to the
		// client, and the log is the only place this can be recorded.
		return
	}
}

// handleSubmitExcel reads a filled-in template and submits it.
//
// It ends in submitDraft, so a spreadsheet is judged by exactly the rules a
// form is judged by. The only thing this function decides is what the file
// says — everything after that is the one pipeline.
func (m *Module) handleSubmitExcel(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}
	periodID := chi.URLParam(r, "id")

	// 16 MB: an eleven-hundred-row workbook is well under a megabyte, and the
	// margin is for the formatting a sender's copy of Excel adds.
	//
	// ParseMultipartForm's argument only bounds what it keeps in memory — the
	// rest spills to a temp file with no limit — so the body is capped first.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		nexus.Error(w, http.StatusBadRequest, "файлыг уншиж чадсангүй")
		return
	}
	upload, header, err := r.FormFile("file")
	if err != nil {
		nexus.Error(w, http.StatusBadRequest, "файл хавсаргана уу")
		return
	}
	defer upload.Close()

	// A workbook is a zip: a few megabytes can inflate to gigabytes, and
	// excelize's own ceiling is 16 GB. The template unpacks to well under 64 MB.
	file, err := excelize.OpenReader(upload, excelize.Options{
		UnzipSizeLimit:    64 << 20,
		UnzipXMLSizeLimit: 16 << 20,
	})
	if err != nil {
		nexus.Error(w, http.StatusBadRequest, "энэ файл Excel биш эсвэл эвдэрсэн байна")
		return
	}
	defer func() { _ = file.Close() }()

	rows, err := file.GetRows(sheetName)
	if err != nil || len(rows) == 0 {
		nexus.Error(w, http.StatusBadRequest,
			"«"+sheetName+"» нэртэй хуудас олдсонгүй — системээс татсан загварыг ашиглана уу")
		return
	}
	if len(rows) < 2 {
		nexus.Error(w, http.StatusBadRequest, "загварт нэг ч мөр бөглөөгүй байна")
		return
	}

	lines := make([]ReportLine, 0, len(rows)-1)
	for i, row := range rows[1:] {
		number := i + 2 // the row as the sender sees it in Excel
		if len(row) < colClosing+1 {
			nexus.Error(w, http.StatusBadRequest,
				fmt.Sprintf("%d-р мөр дутуу байна: %d багана байх ёстой", number, colClosing+1))
			return
		}
		if strings.TrimSpace(row[colSiteID]) == "" {
			continue // a blank line in the middle of a sheet is not an error
		}
		// A row the sender never touched is not a report of zero.
		//
		// The template arrives with the site and the grade already filled in,
		// so "site_id is present" said nothing about whether anybody answered.
		// A company that filled in three of its forty forecourts submitted
		// thirty-seven declarations of zero — counted as reported, added into
		// the national total, and passing validation because a site with no
		// history has nothing to be continuous with (audit §27).
		if untouched(row) {
			continue
		}

		// A cell is a number written by a person in a spreadsheet, so it may
		// carry a thousands separator, a space, or a comma used as the decimal
		// point. Stripping every comma turned `1234,5` — what a Mongolian or
		// Russian locale writes — into `12345`, ten times the figure, with no
		// error anywhere: the balance then failed and the sender could not find
		// the cause in their own workbook (audit §26).
		amount := func(index int) (float64, error) {
			if index >= len(row) {
				return 0, nil
			}
			raw := readNumber(row[index])
			if raw == "" {
				return 0, nil
			}
			return strconv.ParseFloat(raw, 64)
		}
		optional := func(index int) (*float64, error) {
			if index >= len(row) || strings.TrimSpace(row[index]) == "" {
				return nil, nil
			}
			value, err := amount(index)
			if err != nil {
				return nil, err
			}
			return &value, nil
		}

		line := ReportLine{
			SiteKind:    strings.TrimSpace(row[colSiteKind]),
			SiteID:      strings.TrimSpace(row[colSiteID]),
			ProductCode: strings.TrimSpace(row[colProduct]),
		}
		var readErr error
		for _, field := range []struct {
			index  int
			target *float64
			name   string
		}{
			{colOpening, &line.Opening, "нээлтийн үлдэгдэл"},
			{colReceipts, &line.Receipts, "хүлээн авалт"},
			{colSales, &line.Sales, "борлуулалт"},
			{colTransfers, &line.TransfersOut, "шилжүүлэг"},
			{colAdjustments, &line.Adjustments, "залруулга"},
			{colClosing, &line.Closing, "хаалтын үлдэгдэл"},
		} {
			value, err := amount(field.index)
			if err != nil {
				readErr = fmt.Errorf("%d-р мөрийн «%s» тоо биш байна", number, field.name)
				break
			}
			*field.target = value
		}
		if readErr != nil {
			nexus.Error(w, http.StatusBadRequest, readErr.Error())
			return
		}

		for _, field := range []struct {
			index  int
			target **float64
			name   string
		}{
			{colPrice, &line.PriceMNT, "үнэ"},
			{colTemperature, &line.TemperatureC, "температур"},
			{colDensity, &line.DensityKgM3, "нягт"},
		} {
			value, err := optional(field.index)
			if err != nil {
				nexus.Error(w, http.StatusBadRequest,
					fmt.Sprintf("%d-р мөрийн «%s» тоо биш байна", number, field.name))
				return
			}
			*field.target = value
		}
		if colNote < len(row) {
			line.Note = strings.TrimSpace(row[colNote])
		}

		lines = append(lines, line)
	}

	if len(lines) == 0 {
		nexus.Error(w, http.StatusBadRequest, "загварт нэг ч мөр бөглөөгүй байна")
		return
	}

	m.submitDraft(w, r, periodID, SubmissionDraft{
		Source:         "excel",
		FileName:       header.Filename,
		IdempotencyKey: r.FormValue("idempotency_key"),
		Lines:          lines,
	})
}

// readNumber normalises one spreadsheet cell into something ParseFloat reads.
//
// Spaces and non-breaking spaces are thousands separators and go. What a comma
// means depends on where it sits: with a comma and no dot, and two or fewer
// digits after the last comma, it is a decimal point; otherwise it groups.
func readNumber(cell string) string {
	raw := strings.TrimSpace(cell)
	raw = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "").Replace(raw)
	if raw == "" {
		return ""
	}

	lastComma := strings.LastIndex(raw, ",")
	if lastComma >= 0 && !strings.Contains(raw, ".") && len(raw)-lastComma-1 <= 2 {
		return strings.Replace(raw, ",", ".", 1)
	}
	return strings.ReplaceAll(raw, ",", "")
}

// untouched says the sender left this template row exactly as it was issued:
// every figure column blank.
func untouched(row []string) bool {
	for _, index := range []int{colReceipts, colSales, colTransfers, colAdjustments, colClosing} {
		if index < len(row) && strings.TrimSpace(row[index]) != "" {
			return false
		}
	}
	return true
}
