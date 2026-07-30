package keg_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestParseFieldSelector(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantKind keg.FieldKind
		wantKey  string
		wantErr  bool
	}{
		{name: "id intrinsic", raw: "id", wantKind: keg.FieldID, wantKey: "id"},
		{name: "title intrinsic", raw: "title", wantKind: keg.FieldTitle, wantKey: "title"},
		{name: "tags reserved", raw: "tags", wantKind: keg.FieldTags, wantKey: "tags"},
		{name: "bare word is a meta key", raw: "type", wantKind: keg.FieldMetaKey, wantKey: "type"},
		{name: "meta key with spaces inside", raw: "my key", wantKind: keg.FieldMetaKey, wantKey: "my key"},
		{name: "surrounding space trimmed", raw: "  status  ", wantKind: keg.FieldMetaKey, wantKey: "status"},

		// The three index timestamps must classify as FieldIndexTime, not
		// FieldStat, or the default format would start reading stats.json.
		{name: "updated is index time", raw: ".updated", wantKind: keg.FieldIndexTime, wantKey: "updated"},
		{name: "created is index time", raw: ".created", wantKind: keg.FieldIndexTime, wantKey: "created"},
		{name: "accessed is index time", raw: ".accessed", wantKind: keg.FieldIndexTime, wantKey: "accessed"},

		{name: "hash is a stat", raw: ".hash", wantKind: keg.FieldStat, wantKey: "hash"},
		{name: "lead is a stat", raw: ".lead", wantKind: keg.FieldStat, wantKey: "lead"},
		{name: "accessCount is a stat", raw: ".accessCount", wantKind: keg.FieldStat, wantKey: "accessCount"},
		{name: "omega is a stat", raw: ".omega", wantKind: keg.FieldStat, wantKey: "omega"},

		{name: "empty", raw: "", wantErr: true},
		{name: "only spaces", raw: "   ", wantErr: true},
		{name: "unknown stats field", raw: ".bogus", wantErr: true},
		{name: "on-disk spelling is not a selector", raw: ".access_count", wantErr: true},
		{name: "equality predicate", raw: "type=plan", wantErr: true},
		{name: "inequality predicate", raw: "omega>=0.5", wantErr: true},
		{name: "brace in selector", raw: "ty{pe", wantErr: true},
		{name: "percent in selector", raw: "ty%pe", wantErr: true},
		{name: "control character", raw: "ty\npe", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := keg.ParseFieldSelector(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseFieldSelector(%q) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFieldSelector(%q): %v", tc.raw, err)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, tc.wantKind)
			}
			if got.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tc.wantKey)
			}
		})
	}
}

func TestFieldSelectorNeeds(t *testing.T) {
	// Only metadata and non-timestamp stats cost a read. Getting this wrong
	// makes the default format perform per-node I/O.
	tests := []struct {
		raw       string
		wantMeta  bool
		wantStats bool
	}{
		{raw: "id"},
		{raw: "title"},
		{raw: ".updated"},
		{raw: ".created"},
		{raw: ".accessed"},
		{raw: "tags", wantMeta: true},
		{raw: "type", wantMeta: true},
		{raw: ".hash", wantStats: true},
		{raw: ".omega", wantStats: true},
		{raw: ".accessCount", wantStats: true},
		{raw: ".lead", wantStats: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			sel, err := keg.ParseFieldSelector(tc.raw)
			if err != nil {
				t.Fatalf("ParseFieldSelector(%q): %v", tc.raw, err)
			}
			if got := sel.NeedsMeta(); got != tc.wantMeta {
				t.Errorf("NeedsMeta() = %v, want %v", got, tc.wantMeta)
			}
			if got := sel.NeedsStats(); got != tc.wantStats {
				t.Errorf("NeedsStats() = %v, want %v", got, tc.wantStats)
			}
		})
	}
}

func TestStatsFieldValueUnknownField(t *testing.T) {
	if _, known := keg.StatsFieldValue(nil, "nope"); known {
		t.Error("StatsFieldValue(nil, \"nope\") reported known, want unknown")
	}
}

func TestStatsFieldValueNilStats(t *testing.T) {
	// Absent stats render empty, except accessCount, which has no absent
	// state on disk and so always renders its integer.
	for _, name := range keg.StatsFieldNames {
		value, known := keg.StatsFieldValue(nil, name)
		if !known {
			t.Errorf("%s: known = false, want true", name)
			continue
		}
		want := ""
		if name == "accessCount" {
			want = "0"
		}
		if value != want {
			t.Errorf("%s: value = %q, want %q", name, value, want)
		}
	}
}

func TestStatsFieldValuePopulated(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	stats := keg.NewStats(now)
	stats.SetHash("abc123", &now)
	stats.SetLead("a lead line")
	stats.SetAccessCount(7)
	stats.SetOmega(0.75)

	tests := map[string]string{
		"hash":        "abc123",
		"lead":        "a lead line",
		"accessCount": "7",
		"omega":       "0.75",
	}
	for name, want := range tests {
		got, known := keg.StatsFieldValue(stats, name)
		if !known {
			t.Errorf("%s: known = false, want true", name)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestStatsFieldValueOmegaAbsentIsNotZero(t *testing.T) {
	// omega is genuinely tri-state, so absent must not render as "0".
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	stats := keg.NewStats(now)
	stats.ClearOmega()

	got, known := keg.StatsFieldValue(stats, "omega")
	if !known {
		t.Fatal("omega: known = false, want true")
	}
	if got != "" {
		t.Errorf("absent omega = %q, want empty", got)
	}

	stats.SetOmega(0)
	got, _ = keg.StatsFieldValue(stats, "omega")
	if got != "0" {
		t.Errorf("zero omega = %q, want %q", got, "0")
	}
}

func TestStatsFieldValueZeroTimeRendersEmpty(t *testing.T) {
	// A zero time formatted as RFC3339 is 0001-01-01T00:00:00Z, which parses
	// as a real date and would silently corrupt downstream sorting.
	stats := keg.NewStats(time.Time{})
	for _, name := range keg.IndexTimeFieldNames {
		got, known := keg.StatsFieldValue(stats, name)
		if !known {
			t.Errorf("%s: known = false, want true", name)
			continue
		}
		if got != "" {
			t.Errorf("%s zero time = %q, want empty", name, got)
		}
	}
}

func TestIndexTimeFieldsAreStatsFields(t *testing.T) {
	// IndexTimeFieldNames is a subset of StatsFieldNames: the same selector
	// spelling, served from a cheaper source.
	for _, name := range keg.IndexTimeFieldNames {
		sel, err := keg.ParseFieldSelector("." + name)
		if err != nil {
			t.Errorf(".%s is not a valid selector: %v", name, err)
			continue
		}
		if sel.Kind != keg.FieldIndexTime {
			t.Errorf(".%s Kind = %v, want FieldIndexTime", name, sel.Kind)
		}
	}
}

func TestLegacyFormatVerbsResolve(t *testing.T) {
	// Every legacy verb must name a selector that still parses, or the
	// compatibility aliases silently break.
	want := map[byte]keg.FieldKind{
		'i': keg.FieldID,
		't': keg.FieldTitle,
		'd': keg.FieldIndexTime,
		'c': keg.FieldIndexTime,
		'a': keg.FieldIndexTime,
	}
	if len(keg.LegacyFormatVerbs) != len(want) {
		t.Fatalf("LegacyFormatVerbs has %d entries, want %d", len(keg.LegacyFormatVerbs), len(want))
	}
	for verb, text := range keg.LegacyFormatVerbs {
		sel, err := keg.ParseFieldSelector(text)
		if err != nil {
			t.Errorf("%%%c -> %q: %v", verb, text, err)
			continue
		}
		if sel.Kind != want[verb] {
			t.Errorf("%%%c -> %q Kind = %v, want %v", verb, text, sel.Kind, want[verb])
		}
	}
}

func TestFormatSelectorSuggestions(t *testing.T) {
	got := keg.FormatSelectorSuggestions()
	if len(got) != len(keg.ReservedFieldNames)+len(keg.StatsFieldNames) {
		t.Fatalf("suggestions = %d, want %d", len(got), len(keg.ReservedFieldNames)+len(keg.StatsFieldNames))
	}
	for _, s := range got {
		if len(s) < 4 || s[:2] != "%{" || s[len(s)-1] != '}' {
			t.Errorf("suggestion %q is not a ready-to-type token", s)
		}
	}
}

func TestConfigListFieldsRoundTrip(t *testing.T) {
	raw := []byte("kegv: 2025-07\nlistFields:\n  - id\n  - type\n  - subkind\n  - title\n")
	cfg, err := keg.ParseKegConfig(raw)
	if err != nil {
		t.Fatalf("ParseKegConfig: %v", err)
	}
	want := []string{"id", "type", "subkind", "title"}
	if len(cfg.ListFields) != len(want) {
		t.Fatalf("ListFields = %v, want %v", cfg.ListFields, want)
	}
	for i, field := range want {
		if cfg.ListFields[i] != field {
			t.Errorf("ListFields[%d] = %q, want %q", i, cfg.ListFields[i], field)
		}
	}

	// The setting must survive a serialize/parse cycle or editing any other
	// field through the settings form would silently drop it.
	out, err := cfg.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML: %v", err)
	}
	again, err := keg.ParseKegConfig(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if len(again.ListFields) != len(want) {
		t.Errorf("after round trip ListFields = %v, want %v", again.ListFields, want)
	}
}

func TestConfigListFieldsRejectsBadSelector(t *testing.T) {
	// Rejecting at parse time means a typo surfaces when the config is saved
	// rather than as a silently blank column at render time.
	raw := []byte("kegv: 2025-07\nlistFields:\n  - type\n  - .bogus\n")
	if _, err := keg.ParseKegConfig(raw); err == nil {
		t.Fatal("ParseKegConfig accepted an unknown stats selector, want error")
	} else if !strings.Contains(err.Error(), "listFields") {
		t.Errorf("error = %q, want it to name the offending field", err)
	}
}

func TestConfigListFieldsEmptyIsValid(t *testing.T) {
	if _, err := keg.ParseKegConfig([]byte("kegv: 2025-07\n")); err != nil {
		t.Fatalf("config without listFields should parse: %v", err)
	}
}
