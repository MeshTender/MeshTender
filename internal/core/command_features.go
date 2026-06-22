package core

import (
	"sort"

	"github.com/jleight/meshtender/internal/store"
)

// Presentation grouping for the permission review/consent and catalog UIs. Each
// command's feature area and operation (read/write/delete/action) live on the
// catalog row (store.Command.Feature/.Operation, set in the DB); this file only
// orders and buckets them. The security model (arity, per-command auth) is
// unaffected.

// featureOrder is the display order of feature groups. Features not listed here
// (e.g. a newly introduced one) sort after these, alphabetically — so nothing is
// ever dropped from the UI even before this list is updated.
var featureOrder = []string{"Radio", "Routing", "Advertising", "Location", "GPS", "Clock",
	"Region", "Neighbors", "Sensors", "Identity", "Access", "Power", "Diagnostics", "Firmware"}

// featureRank gives a feature's position in featureOrder, or a large value
// (sorting it after the known features) when it isn't listed.
func featureRank(f string) int {
	for i, x := range featureOrder {
		if x == f {
			return i
		}
	}
	return len(featureOrder)
}

// orderFeatures sorts the distinct feature names by featureOrder, with unknown
// features appended alphabetically.
func orderFeatures(present []string) {
	sort.SliceStable(present, func(i, j int) bool {
		ri, rj := featureRank(present[i]), featureRank(present[j])
		if ri != rj {
			return ri < rj
		}
		return present[i] < present[j]
	})
}

// cmdCell is one command shown in a feature-table cell.
type cmdCell struct {
	Template    string
	Description string // shown as a hover tooltip
	Risky       bool
}

// featureRow is one feature's commands bucketed by operation, for the review UI.
type featureRow struct {
	Feature                     string
	Read, Write, Delete, Action []cmdCell
}

// featureTableFor groups the commands in `allowed` (the id-set a single role may
// run) by feature × operation, ordered by featureOrder — one table per role.
func featureTableFor(catalog []*store.Command, allowed map[int64]bool) []featureRow {
	byFeature := map[string]*featureRow{}
	var present []string
	for _, c := range catalog {
		if !allowed[c.ID] {
			continue
		}
		row := byFeature[c.Feature]
		if row == nil {
			row = &featureRow{Feature: c.Feature}
			byFeature[c.Feature] = row
			present = append(present, c.Feature)
		}
		cell := cmdCell{Template: c.Template, Description: c.Description, Risky: c.Risky}
		switch c.Operation {
		case "read":
			row.Read = append(row.Read, cell)
		case "delete":
			row.Delete = append(row.Delete, cell)
		case "action":
			row.Action = append(row.Action, cell)
		default: // "write"
			row.Write = append(row.Write, cell)
		}
	}
	orderFeatures(present)
	out := make([]featureRow, 0, len(present))
	for _, f := range present {
		out = append(out, *byFeature[f])
	}
	return out
}
