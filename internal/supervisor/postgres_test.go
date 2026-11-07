package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpCommandScrubsPassword(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pg_dump")
	script := "#!/bin/sh\necho \"failed $*\" >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	err := DumpCommand(bin, "postgres://togavsu:synthetic-postgres-password@127.0.0.1/togavsu")(context.Background(), ioDiscard{})
	if err == nil {
		t.Fatal("expected dump failure")
	}
	if strings.Contains(err.Error(), "synthetic-postgres-password") {
		t.Fatalf("password leaked: %v", err)
	}
}

func TestQuoteTableRejectsInjection(t *testing.T) {
	if _, err := quoteTable(`core.matters;drop`); err == nil {
		t.Fatal("accepted injected table")
	}
	got, err := quoteTable("core.matters")
	if err != nil || got != `"core"."matters"` {
		t.Fatalf("%s %v", got, err)
	}
}

func TestSwapRenamePairReconcilesAmbiguousCommits(t *testing.T) {
	cases := []struct {
		mode    string
		wantErr func(error) bool
		present []string
		absent  []string
	}{
		{
			mode:    "fail-before",
			wantErr: func(err error) bool { return err != nil && !errors.Is(err, ErrRestoreCleanup) },
			present: []string{"togavsu", "togavsu_restore_ab"},
			absent:  []string{"togavsu_replaced_ab"},
		},
		{
			mode:    "fail-between",
			wantErr: func(err error) bool { return err != nil && !errors.Is(err, ErrRestoreCleanup) },
			present: []string{"togavsu", "togavsu_restore_ab"},
			absent:  []string{"togavsu_replaced_ab"},
		},
		{
			mode: "lose-first",
			wantErr: func(err error) bool {
				return err != nil && !errors.Is(err, ErrRestoreCleanup) && !errors.Is(err, errConfiguredDatabaseAbsent)
			},
			present: []string{"togavsu", "togavsu_restore_ab"},
			absent:  []string{"togavsu_replaced_ab"},
		},
		{
			mode:    "lose-commit",
			wantErr: func(err error) bool { return err == nil },
			present: []string{"togavsu"},
			absent:  []string{"togavsu_restore_ab", "togavsu_replaced_ab"},
		},
		{
			mode:    "fail-drop",
			wantErr: func(err error) bool { return errors.Is(err, ErrRestoreCleanup) },
			present: []string{"togavsu", "togavsu_replaced_ab"},
			absent:  []string{"togavsu_restore_ab"},
		},
		{
			mode:    "lose-drop",
			wantErr: func(err error) bool { return err == nil },
			present: []string{"togavsu"},
			absent:  []string{"togavsu_restore_ab", "togavsu_replaced_ab"},
		},
		{
			mode:    "blind",
			wantErr: func(err error) bool { return errors.Is(err, errSwapUnobserved) },
			present: []string{"togavsu"},
			absent:  []string{"togavsu_restore_ab"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			bin, state, logPath := writeCatalogPsql(t, tc.mode)
			err := swapDatabase(context.Background(), bin, "postgres://togavsu@127.0.0.1/postgres", "togavsu", "togavsu_restore_ab", "ab")
			if !tc.wantErr(err) {
				t.Fatalf("got %v", err)
			}
			got := readCatalog(t, state)
			if !got["togavsu"] {
				t.Fatalf("configured database absent: %v err %v", got, err)
			}
			for _, name := range tc.present {
				if !got[name] {
					t.Fatalf("missing %s in %v", name, got)
				}
			}
			for _, name := range tc.absent {
				if got[name] {
					t.Fatalf("%s still present in %v", name, got)
				}
			}
			if tc.mode == "fail-drop" {
				raw, readErr := os.ReadFile(logPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				log := string(raw)
				if !strings.Contains(log, "ALTER DATABASE togavsu RENAME TO togavsu_replaced_ab") || !strings.Contains(log, "ALTER DATABASE togavsu_restore_ab RENAME TO togavsu") {
					t.Fatalf("renames did not both commit before the drop failed: %s", log)
				}
			}
		})
	}
}

func TestSwapCanceledCallerLeavesConfiguredDatabase(t *testing.T) {
	bin, state, _ := writeCatalogPsql(t, "fail-before")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := swapDatabase(ctx, bin, "postgres://togavsu@127.0.0.1/postgres", "togavsu", "togavsu_restore_ab", "ab")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	got := readCatalog(t, state)
	if !got["togavsu"] || !got["togavsu_restore_ab"] || got["togavsu_replaced_ab"] {
		t.Fatalf("catalog %v", got)
	}
}

func writeCatalogPsql(t *testing.T, mode string) (bin, state, logPath string) {
	t.Helper()
	dir := t.TempDir()
	state = filepath.Join(dir, "names")
	logPath = filepath.Join(dir, "sql.log")
	if err := os.WriteFile(state, []byte("togavsu\ntogavsu_restore_ab\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(catalogPsqlScript, "__MODE__", mode)
	script = strings.ReplaceAll(script, "__STATE__", state)
	script = strings.ReplaceAll(script, "__LOG__", logPath)
	bin = filepath.Join(dir, "psql")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, state, logPath
}

func readCatalog(t *testing.T, state string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line != "" {
			got[line] = true
		}
	}
	return got
}

const catalogPsqlScript = `#!/bin/sh
MODE='__MODE__'
STATE='__STATE__'
LOG='__LOG__'
cmd=
prev=
for arg in "$@"; do
  if [ "$prev" = "--command" ]; then
    cmd=$arg
  fi
  prev=$arg
done
printf '%s\n' "$cmd" >> "$LOG"
apply() {
  from=$1
  to=$2
  grep -vx "$from" "$STATE" > "$STATE.next" || true
  printf '%s\n' "$to" >> "$STATE.next"
  mv "$STATE.next" "$STATE"
}
drop_name() {
  grep -vx "$1" "$STATE" > "$STATE.next" || true
  mv "$STATE.next" "$STATE"
}
case "$cmd" in
  "SELECT datname"*)
    if [ "$MODE" = "blind" ]; then
      echo "connection reset" >&2
      exit 1
    fi
    while IFS= read -r n; do
      [ -n "$n" ] && printf '%s\n' "$n"
    done < "$STATE"
    exit 0
    ;;
  BEGIN*)
    case "$MODE" in
      fail-before|fail-between)
        echo "refused" >&2
        exit 1
        ;;
      lose-first)
        apply togavsu togavsu_replaced_ab
        echo "lost acknowledgment" >&2
        exit 1
        ;;
      lose-commit)
        apply togavsu togavsu_replaced_ab
        apply togavsu_restore_ab togavsu
        echo "lost acknowledgment" >&2
        exit 1
        ;;
      *)
        apply togavsu togavsu_replaced_ab
        apply togavsu_restore_ab togavsu
        exit 0
        ;;
    esac
    ;;
  "ALTER DATABASE "*)
    from=$(printf '%s\n' "$cmd" | awk '{print $3}')
    to=$(printf '%s\n' "$cmd" | awk '{print $6}')
    apply "$from" "$to"
    exit 0
    ;;
  "DROP DATABASE "*)
    name=$(printf '%s\n' "$cmd" | awk '{print $3}')
    if [ "$MODE" = "fail-drop" ]; then
      echo "drop refused" >&2
      exit 1
    fi
    if [ "$MODE" = "lose-drop" ]; then
      drop_name "$name"
      echo "lost acknowledgment" >&2
      exit 1
    fi
    drop_name "$name"
    exit 0
    ;;
esac
exit 0
`

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
