package core

import "sort"

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

// This file keeps the feature ordering used by groupByFeature for the catalog and
// share/org command-selection groupings.
