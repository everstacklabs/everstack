package activity

import "sort"

func sortedKeys(rows map[rowKey]row) []rowKey {
	keys := make([]rowKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.key != b.key {
			return a.key < b.key
		}
		if a.day != b.day {
			return a.day < b.day
		}
		return a.surface < b.surface
	})
	return keys
}
