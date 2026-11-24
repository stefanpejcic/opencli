package diskquota

import "testing"

func TestParseRepquota(t *testing.T) {
	out := `*** Report for project quotas on device /dev/loop0
Block grace time: 7days; Inode grace time: 7days
                        Block limits                File limits
Project         used    soft    hard  grace    used  soft  hard  grace
----------------------------------------------------------------------
#0        --    5148       0       0              6     0     0
#1003     --       4    2048    2048              1    50    50
#1004     +-    3000    2048    4096  6days       7    50    50
#1005     -+      10    2048    2048             60    50    90  7days

`
	rows := parseRepquota(out)
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4: %v", len(rows), rows)
	}
	want := map[string]Row{
		"#1003": {"4", "2048", "2048", "1", "50", "50"},
		"#1004": {"3000", "2048", "4096", "7", "50", "50"},
		"#1005": {"10", "2048", "2048", "60", "50", "90"},
	}
	for k, w := range want {
		if rows[k] != w {
			t.Errorf("%s = %+v, want %+v", k, rows[k], w)
		}
	}
}
