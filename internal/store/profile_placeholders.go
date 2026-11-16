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

// Profile command lines can reference the org's radio settings and path hash
// mode through inline placeholders instead of hard-coding them, e.g.
//
//	set radio {{ radio.freq }},{{ radio.bw }},{{ radio.sf }},{{ radio.cr }}
//	set path.hash.mode {{ path.hash.mode }}
//
// They're stored in canonical form (CanonicalizePlaceholders) and filled in by
// ExpandPlaceholders wherever a profile is assembled into commands: the org
// Configuration page, the console's "apply org configuration" and serial setup.
// Unlike RegionMarker, which is a whole step, these live inside a command line.

// placeholderRE matches one {{ ... }} reference, tolerating any inner spacing.
var placeholderRE = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

// OrgMesh is what an org's profile placeholders are filled from: its radio
// settings and its path hash mode, each nil until an admin sets it.
type OrgMesh struct {
	Radio        *config.RadioDefaults
	PathHashMode *int // 0, 1 or 2: 1-, 2- or 3-byte path hashes (`set path.hash.mode`)
}

// placeholders maps each placeholder name to the value it stands for — the
// radio in the units `set radio` takes (MHz, kHz), the path hash mode as the
// number `set path.hash.mode` takes. ok is false when the org hasn't set what
// it needs.
var placeholders = map[string]func(OrgMesh) (string, bool){
	"radio.freq": radioValue(config.RadioDefaults.MHz),
	"radio.bw":   radioValue(config.RadioDefaults.KHz),
	"radio.sf":   radioValue(func(r config.RadioDefaults) string { return strconv.Itoa(int(r.SF)) }),
	"radio.cr":   radioValue(func(r config.RadioDefaults) string { return strconv.Itoa(int(r.CR)) }),
	"path.hash.mode": func(m OrgMesh) (string, bool) {
		if m.PathHashMode == nil {
			return "", false
		}
		return strconv.Itoa(*m.PathHashMode), true
	},
}

// radioValue lifts a radio formatter into a placeholder that needs the radio set.
func radioValue(f func(config.RadioDefaults) string) func(OrgMesh) (string, bool) {
	return func(m OrgMesh) (string, bool) {
		if m.Radio == nil {
			return "", false
		}
		return f(*m.Radio), true
	}
}

// ErrRadioInUse / ErrPathHashInUse are returned when clearing an org's radio
// settings or path hash mode while a profile still references them.
var (
	ErrRadioInUse    = errors.New("store: radio settings are used by a profile")
	ErrPathHashInUse = errors.New("store: path hash mode is used by a profile")
)

// CanonicalizePlaceholders rewrites every {{ name }} in line to the stored form
// "{{ radio.freq }}" (single spaces, lower case) and reports any names that
// aren't placeholders, in the form they were typed.
func CanonicalizePlaceholders(line string) (canonical string, unknown []string) {
	canonical = placeholderRE.ReplaceAllStringFunc(line, func(m string) string {
		name := strings.ToLower(strings.TrimSpace(m[2 : len(m)-2]))
		if _, ok := placeholders[name]; !ok {
			unknown = append(unknown, m)
			return m
		}
		return "{{ " + name + " }}"
	})
	return canonical, unknown
}

// HasPlaceholders reports whether a line references any placeholder.
func HasPlaceholders(line string) bool { return placeholderRE.MatchString(line) }

// ExpandPlaceholders fills a canonical line's placeholders from mesh. ok is
// false — and the line comes back untouched — when it uses a setting the org
// hasn't set, or names a placeholder that doesn't exist.
func ExpandPlaceholders(line string, mesh OrgMesh) (string, bool) {
	if !HasPlaceholders(line) {
		return line, true
	}
	ok := true
	out := placeholderRE.ReplaceAllStringFunc(line, func(m string) string {
		value, known := placeholders[strings.ToLower(strings.TrimSpace(m[2:len(m)-2]))]
		if !known {
			ok = false
			return m
		}
		filled, set := value(mesh)
		if !set {
			ok = false
			return m
		}
		return filled
	})
	if !ok {
		return line, false
	}
	return out, true
}

// ExpandSteps fills the placeholders in every command step. A step that can't be
// filled is kept as written and ok is false, so a caller can still show the
// profile while refusing to run it.
func ExpandSteps(steps []ConfigStep, mesh OrgMesh) (out []ConfigStep, ok bool) {
	ok = true
	out = make([]ConfigStep, len(steps))
	for i, st := range steps {
		out[i] = st
		if st.IsComment() || st.IsRegionMarker() {
			continue
		}
		line, filled := ExpandPlaceholders(st.CommandLine, mesh)
		out[i].CommandLine = line
		ok = ok && filled
	}
	return out, ok
}

// radioPlaceholderLike / pathHashPlaceholderLike match a stored step that
// references the org's radio or path hash mode. Steps are saved canonical, so
// the patterns are exact.
const (
	radioPlaceholderLike    = `%{{ radio.%`
	pathHashPlaceholderLike = `%{{ path.hash.mode }}%`
)

// OrgMesh returns what an org's profile placeholders are filled from.
func (s *Store) OrgMesh(ctx context.Context, orgID int64) (OrgMesh, error) {
	radio, err := s.OrgRadio(ctx, orgID)
	if err != nil {
		return OrgMesh{}, err
	}
	mode, err := s.OrgPathHashMode(ctx, orgID)
	if err != nil {
		return OrgMesh{}, err
	}
	return OrgMesh{Radio: radio, PathHashMode: mode}, nil
}

// OrgPathHashMode returns an org's path hash mode, or nil when it hasn't set one.
func (s *Store) OrgPathHashMode(ctx context.Context, orgID int64) (*int, error) {
	var mode *int16
	if err := s.pool.QueryRow(ctx, `SELECT path_hash_mode FROM organizations WHERE id = $1`, orgID).Scan(&mode); err != nil {
		return nil, notFoundOr(err, "org path hash mode")
	}
	if mode == nil {
		return nil, nil
	}
	m := int(*mode)
	return &m, nil
}

// SetOrgPathHashMode stores an org's path hash mode (0–2), or clears it when
// mode is nil. Clearing is refused with ErrPathHashInUse while a profile uses
// {{ path.hash.mode }} — checked in the same statement, as SetOrgRadio does.
func (s *Store) SetOrgPathHashMode(ctx context.Context, orgID int64, mode *int) error {
	if mode != nil {
		if *mode < 0 || *mode > 2 {
			return fmt.Errorf("path hash mode %d: want 0, 1 or 2", *mode)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE organizations SET path_hash_mode = $2 WHERE id = $1`, orgID, int16(*mode)); err != nil {
			return fmt.Errorf("set org path hash mode: %w", err)
		}
		return nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE organizations SET path_hash_mode = NULL
		WHERE id = $1 AND NOT EXISTS (
			SELECT 1 FROM config_profile_steps s JOIN config_profiles p ON p.id = s.profile_id
			WHERE p.org_id = $1 AND s.command_line LIKE $2)`, orgID, pathHashPlaceholderLike)
	if err != nil {
		return fmt.Errorf("clear org path hash mode: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPathHashInUse
	}
	return nil
}

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
