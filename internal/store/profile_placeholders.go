package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MeshTender/MeshTender/internal/config"
)

// Profile command lines can reference the org's radio settings through inline
// placeholders instead of hard-coding them, e.g.
//
//	set radio {{ radio.freq }},{{ radio.bw }},{{ radio.sf }},{{ radio.cr }}
//
// They're stored in canonical form (CanonicalizePlaceholders) and filled in by
// ExpandPlaceholders wherever a profile is assembled into commands: the org
// Configuration page, the console's "apply org configuration" and serial setup.
// Unlike RegionMarker, which is a whole step, these live inside a command line.

// placeholderRE matches one {{ ... }} reference, tolerating any inner spacing.
var placeholderRE = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

// radioPlaceholders maps each placeholder name to the value it stands for, in
// the units `set radio` takes (MHz, kHz).
var radioPlaceholders = map[string]func(config.RadioDefaults) string{
	"radio.freq": config.RadioDefaults.MHz,
	"radio.bw":   config.RadioDefaults.KHz,
	"radio.sf":   func(r config.RadioDefaults) string { return strconv.Itoa(int(r.SF)) },
	"radio.cr":   func(r config.RadioDefaults) string { return strconv.Itoa(int(r.CR)) },
}

// RadioPlaceholderNames lists the placeholders in the order `set radio` takes
// them, for help text.
var RadioPlaceholderNames = []string{"radio.freq", "radio.bw", "radio.sf", "radio.cr"}

// ErrRadioInUse is returned when clearing an org's radio settings that a
// profile still references.
var ErrRadioInUse = errors.New("store: radio settings are used by a profile")

// CanonicalizePlaceholders rewrites every {{ name }} in line to the stored form
// "{{ radio.freq }}" (single spaces, lower case) and reports any names that
// aren't placeholders, in the form they were typed.
func CanonicalizePlaceholders(line string) (canonical string, unknown []string) {
	canonical = placeholderRE.ReplaceAllStringFunc(line, func(m string) string {
		name := strings.ToLower(strings.TrimSpace(m[2 : len(m)-2]))
		if _, ok := radioPlaceholders[name]; !ok {
			unknown = append(unknown, m)
			return m
		}
		return "{{ " + name + " }}"
	})
	return canonical, unknown
}

// HasPlaceholders reports whether a line references any placeholder.
func HasPlaceholders(line string) bool { return placeholderRE.MatchString(line) }

// ExpandPlaceholders fills a canonical line's placeholders from radio. ok is
// false — and the line comes back untouched — when it has placeholders but the
// org's radio isn't set, or it names one that doesn't exist.
func ExpandPlaceholders(line string, radio *config.RadioDefaults) (string, bool) {
	if !HasPlaceholders(line) {
		return line, true
	}
	if radio == nil {
		return line, false
	}
	ok := true
	out := placeholderRE.ReplaceAllStringFunc(line, func(m string) string {
		value, known := radioPlaceholders[strings.ToLower(strings.TrimSpace(m[2:len(m)-2]))]
		if !known {
			ok = false
			return m
		}
		return value(*radio)
	})
	if !ok {
		return line, false
	}
	return out, true
}

// ExpandSteps fills the placeholders in every command step. A step that can't be
// filled is kept as written and ok is false, so a caller can still show the
// profile while refusing to run it.
func ExpandSteps(steps []ConfigStep, radio *config.RadioDefaults) (out []ConfigStep, ok bool) {
	ok = true
	out = make([]ConfigStep, len(steps))
	for i, st := range steps {
		out[i] = st
		if st.IsComment() || st.IsRegionMarker() {
			continue
		}
		line, filled := ExpandPlaceholders(st.CommandLine, radio)
		out[i].CommandLine = line
		ok = ok && filled
	}
	return out, ok
}

// radioPlaceholderLike matches a stored step that references the org's radio.
// Steps are saved canonical, so the prefix is exact.
const radioPlaceholderLike = `%{{ radio.%`

// OrgRadio returns an org's radio settings, or nil when it has none.
func (s *Store) OrgRadio(ctx context.Context, orgID int64) (*config.RadioDefaults, error) {
	var freq, bw *int64
	var sf, cr *int16
	if err := s.pool.QueryRow(ctx,
		`SELECT radio_freq_hz, radio_bw_hz, radio_sf, radio_cr FROM organizations WHERE id = $1`, orgID).
		Scan(&freq, &bw, &sf, &cr); err != nil {
		return nil, notFoundOr(err, "org radio")
	}
	if freq == nil {
		return nil, nil // the CHECK keeps the four columns all-or-none
	}
	return &config.RadioDefaults{FreqHz: uint32(*freq), BwHz: uint32(*bw), SF: uint8(*sf), CR: uint8(*cr)}, nil //nolint:gosec // G115: written through SetOrgRadio from a Valid radio
}

// SetOrgRadio stores an org's radio settings, or clears them when radio is nil.
// Clearing is refused with ErrRadioInUse while any of the org's profiles
// references them — checked in the same statement, so a profile saved
// concurrently can't slip between the check and the write.
func (s *Store) SetOrgRadio(ctx context.Context, orgID int64, radio *config.RadioDefaults) error {
	if radio != nil {
		if _, err := s.pool.Exec(ctx,
			`UPDATE organizations SET radio_freq_hz = $2, radio_bw_hz = $3, radio_sf = $4, radio_cr = $5 WHERE id = $1`,
			orgID, int64(radio.FreqHz), int64(radio.BwHz), int16(radio.SF), int16(radio.CR)); err != nil {
			return fmt.Errorf("set org radio: %w", err)
		}
		return nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE organizations SET radio_freq_hz = NULL, radio_bw_hz = NULL, radio_sf = NULL, radio_cr = NULL
		WHERE id = $1 AND NOT EXISTS (
			SELECT 1 FROM config_profile_steps s JOIN config_profiles p ON p.id = s.profile_id
			WHERE p.org_id = $1 AND s.command_line LIKE $2)`, orgID, radioPlaceholderLike)
	if err != nil {
		return fmt.Errorf("clear org radio: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRadioInUse
	}
	return nil
}
