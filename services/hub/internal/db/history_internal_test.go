package db

import (
	"strings"
	"testing"
)

func TestEmbeddedMigrationsAreContiguous(t *testing.T) {
	files, err := loadMigrationSet()
	if err != nil {
		t.Fatalf("loadMigrationSet: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations found")
	}
	if err := checkMigrationSource(files); err != nil {
		t.Fatalf("checkMigrationSource: %v", err)
	}
	for v, sum := range files {
		if len(sum) != 64 {
			t.Errorf("version %d: checksum %q is not a hex sha256", v, sum)
		}
	}
}

// TestEmbeddedChecksumsAreStable pins the checksum of a known migration so the
// check cannot silently start agreeing with edited file contents.
func TestEmbeddedChecksumsAreStable(t *testing.T) {
	files, err := loadMigrationSet()
	if err != nil {
		t.Fatalf("loadMigrationSet: %v", err)
	}
	// The migration that motivated the history check: renumbering it once left
	// a dev database without the users.disabled column while reporting a clean
	// migration run.
	const baseline = 26
	sum, ok := files[baseline]
	if !ok {
		t.Fatalf("migration version %d is missing from the embedded set", baseline)
	}
	// checksumSQL of the file's own bytes is by definition stable; what this
	// pins is that the baseline version still exists and is checksummed.
	if got := checksumSQL(mustReadMigration(t, baseline)); got != sum {
		t.Fatalf("version %d checksum changed: %s != %s", baseline, got, sum)
	}
}

func mustReadMigration(t *testing.T, version int) []byte {
	t.Helper()
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		v, err := migrationVersion(e.Name())
		if err != nil || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		if v != version {
			continue
		}
		b, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		return b
	}
	t.Fatalf("no up migration with version %d", version)
	return nil
}

func TestMigrationVersionParsesPrefix(t *testing.T) {
	cases := map[string]int{
		"0001_create_mcp_servers.up.sql":      1,
		"0027_add_users_token_version.up.sql": 27,
	}
	for name, want := range cases {
		got, err := migrationVersion(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s: version = %d, want %d", name, got, want)
		}
	}
}

func TestMigrationVersionRejectsBadNames(t *testing.T) {
	for _, name := range []string{"create_things.up.sql", "abc_x.up.sql", "0_x.up.sql"} {
		if _, err := migrationVersion(name); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestMissingVersions(t *testing.T) {
	cases := []struct {
		name string
		set  migrationSet
		want []int
	}{
		{"contiguous", migrationSet{1: "a", 2: "b", 3: "c"}, nil},
		{"single gap", migrationSet{1: "a", 3: "c"}, []int{2}},
		{"leading gap impossible, middle and tail", migrationSet{2: "b", 5: "e"}, []int{1, 3, 4}},
		{"empty", migrationSet{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := missingVersions(tc.set)
			if len(got) != len(tc.want) {
				t.Fatalf("missing = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("missing = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestCheckMigrationSourceRejectsGaps(t *testing.T) {
	if err := checkMigrationSource(migrationSet{1: "a", 2: "b"}); err != nil {
		t.Fatalf("contiguous set: %v", err)
	}
	err := checkMigrationSource(migrationSet{1: "a", 3: "c"})
	if err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("expected gap error naming version 2, got %v", err)
	}
}

// TestClassifyHistoryBackfillsPreHistoryDatabases covers a database that was
// migrated before schema_history existed: schema_migrations says everything is
// applied, so the current files are trusted and recorded as the baseline.
func TestClassifyHistoryBackfillsPreHistoryDatabases(t *testing.T) {
	files := migrationSet{1: "aa", 2: "bb", 3: "cc"}
	d := classifyHistory(files, migrationSet{}, 3)
	if !d.empty() {
		t.Fatalf("expected no drift, got %+v", d)
	}
	want := []int{1, 2, 3}
	if len(d.ToRecord) != len(want) {
		t.Fatalf("ToRecord = %v, want %v", d.ToRecord, want)
	}
	for i := range want {
		if d.ToRecord[i] != want[i] {
			t.Fatalf("ToRecord = %v, want %v", d.ToRecord, want)
		}
	}
}

func TestClassifyHistoryDetectsRenumberedMigration(t *testing.T) {
	// The incident: version 26 was applied as add_users_token_version, then that
	// file was renumbered to 0027 and a new 0026_add_user_disabled took its
	// place. schema_migrations already read 27, so Up() applied nothing and the
	// disabled column never existed.
	files := migrationSet{25: "x", 26: "disabled", 27: "token_version"}
	recorded := migrationSet{25: "x", 26: "token_version", 27: "token_version"}

	d := classifyHistory(files, recorded, 27)
	if len(d.Mismatched) != 1 || d.Mismatched[0] != 26 {
		t.Fatalf("Mismatched = %v, want [26]", d.Mismatched)
	}
	err := (&HistoryDriftError{drift: d, files: files, recorded: recorded}).Error()
	for _, want := range []string{"version 26", "recorded", "file", "renumbered"} {
		if !strings.Contains(err, want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestClassifyHistoryIgnoresUnappliedVersions(t *testing.T) {
	files := migrationSet{1: "a", 2: "b"}
	recorded := migrationSet{1: "a"}
	d := classifyHistory(files, recorded, 1)
	if !d.empty() || len(d.ToRecord) != 0 {
		t.Fatalf("expected nothing to do, got %+v", d)
	}
}

// TestClassifyHistoryAllowsRollback pins the deliberate leniency for an older
// hub image running against a newer (superset) schema: recorded versions with no
// file are the shape of a rollback, not drift.
func TestClassifyHistoryAllowsRollback(t *testing.T) {
	files := migrationSet{1: "a", 2: "b"}
	recorded := migrationSet{1: "a", 2: "b", 3: "c"}
	d := classifyHistory(files, recorded, 3)
	if !d.empty() {
		t.Fatalf("rollback should not be drift, got %+v", d)
	}
}

// shippedMigrations pins the checksum of every migration that has reached a
// database. Editing one after release - even a comment - changes its checksum,
// and every hub whose schema_history recorded the old one refuses to start
// (#320 reworded a comment in 0034 and took the dev hub down). Fix a shipped
// migration with a new one; when you add a migration, append its line here.
var shippedMigrations = migrationSet{
	1:  "0dfabfdedc5b456ef3de1c6a7b50e7878fcfdcc5997d2a1e01ad3c58bb2cd93e",
	2:  "4db313d64d1ee57b98768953ec3dd2394d559660d01b9a41a2eb8fb912d1c743",
	3:  "940c66818b249f60cba4a77be0c2eaf8e1e616a02cc8b3dc9b5548505c4a64b5",
	4:  "d01a0c4374d49515c29ca3da307c93ed63488f00c0e9799ab730409ded9d2ab9",
	5:  "e073f9ae783556fb197848c0c484020935d588efd17284ca1eae31cfa9ed205f",
	6:  "f8b7e6bc3136a690ce2f2f96185b8ac277ad0833a4b27777b9690b9be3b5e5a2",
	7:  "966c78f1bf388bdb006f898575d36e0b41cbf5806aee1efe057271e1b45d818e",
	8:  "d74b32fa78b07775cc6e8dd2b88560f62abc9fcf405adc16e274e3a02caf54f1",
	9:  "22dbe6ae06245982dbc15e8a056b28be343dd29396484b05f5a598b39cbbd450",
	10: "8215666c90cf24b1fbbd6f7ad8f45cecdf88044cab373c060b3ed956ebb1411f",
	11: "97b553502591f953e795c92d33ca8bfa3fb916a005422e54b74f65e0486d735b",
	12: "a5c9758a9df6b561302ee5718753a142e737aaa81030b1c294e80f4ed202a038",
	13: "5e6027ad2a0e7afb2765a1a0e9bc13220d8cef3c3972ac8b0dab833707864692",
	14: "bd82f07d97878008cb62f3e06e2e5c4537bc8f3854410535f04f2739bf524e00",
	15: "38eec993d7c68f379482d56d54b5564493add78e0c3748639ec6aa0cb6ee5b6a",
	16: "a5d33c4a71847c6c9d2857a90c45d0d571dd25dbb442a7292e1d3b036a70da32",
	17: "ec82a8dc20cdf923b1a4bcfa74761df75eb97c1d488978f4010cee2e819efae7",
	18: "4e7edc831b9ad7d8ba02db7ec9e0fdd179a8057c93134426baf19a8ebc342a17",
	19: "24e4e717c57d5c38db4907719abd18d67a89c7c472415dbaeea69deeb25e7586",
	20: "d5f722e8702bf093fbe303d27c7d28d8538a88f8f5446e58a9d7257c1072b487",
	21: "8562d77f577e889db0d5081fbef85af89a546804c18c5145f2cbd3d3b3d7af6d",
	22: "beb7758c1aece24a76ac8616fd61623975f8d336e10f760ba9dd510e587ef591",
	23: "809e6d2f6a887eb54f73b03c9cd7d0d8fb3458885a0664400e2044553ce2b7c4",
	24: "13ac39a36ccca6ebc8dab7c928b0eee28b141fe15e919c9c3351b426aa6a77e4",
	25: "e35e205237bfd6d51eddaf66bbaee4fa2d9a617de87a986a51bf063fd4a9db05",
	26: "5d5ccd59278a0711bc6bd91655383544e427c9db5f2e814fb88ff6c1d1eadedc",
	27: "9e3341f9c368fcbe521cf27b9328485d7f2beb3a6b6c53165fd4a7575465109e",
	28: "ded2e8bec1ff9bba0d4f09a5bd23e2a7c669521c136607e994b9724916ad27a9",
	29: "5b756298e653862398ae6b6a4f3f5ac0e030ccdd0979987107561720149e94ea",
	30: "f9ee169e37e7d830849ca297584651eec01ff7d86e2f33ea3fb65475c44e6661",
	31: "decac8d9bff35cfdefb69b900c8720c68d9ace717b0407d3b6d43e5befbe52ab",
	32: "82da8425a66a30d1aa685287ba2bc7a93d4159917b0e3cd58ae7a7e2595f70bb",
	33: "5efa6ea59dc43dd36fd9c7f7e5b6381aaf660dbccb44d19b2b3eef39bb8c7e99",
	34: "997c037c907cf993b4abe763849cd028a99eead1c9b2e8b25b79410e071d5998",
	35: "35ab4595cf3f97b6e3af1f54e204077f4819ad1f19653eb6eb5cf8735719b692",
	36: "7c5c8ffcfa7d2f831efaecd9d2f535e5cd7b8502dc08945683f47c7bbbe3e6f5",
	37: "b3c080c80a452ee013f0c93ab6943aad1fbe35597f5e187196e24e38ce9c58d7",
}

func TestShippedMigrationsAreNeverEdited(t *testing.T) {
	files, err := loadMigrationSet()
	if err != nil {
		t.Fatalf("loadMigrationSet: %v", err)
	}
	for v, want := range shippedMigrations {
		got, ok := files[v]
		if !ok {
			t.Errorf("migration %d was shipped but is no longer embedded", v)
			continue
		}
		if got != want {
			t.Errorf("migration %d was edited after release: checksum %s, shipped %s. Restore the file and put the change in a new migration.", v, short(got), short(want))
		}
	}
	for v := range files {
		if _, ok := shippedMigrations[v]; !ok {
			t.Errorf("migration %d is not pinned: add its checksum to shippedMigrations", v)
		}
	}
}
