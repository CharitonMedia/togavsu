package supervisor

import (
	"bufio"
	"io"
	"strings"
)

// countsFromDump reads pg_dump COPY blocks. The counts match the dump's
// snapshot, including rows committed before pg_dump and excluding writes
// that land after it.
func countsFromDump(r io.Reader) (map[string]int64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 32<<20)
	counts := map[string]int64{}
	var table string
	inCopy := false
	for sc.Scan() {
		line := sc.Text()
		if inCopy {
			if line == `\.` {
				inCopy = false
				table = ""
				continue
			}
			counts[table]++
			continue
		}
		if strings.HasPrefix(line, "COPY ") && strings.HasSuffix(line, " FROM stdin;") {
			table = copyTableName(line)
			if table == "" {
				return nil, ErrArchive
			}
			if _, ok := counts[table]; !ok {
				counts[table] = 0
			}
			inCopy = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

func copyTableName(line string) string {
	rest := strings.TrimPrefix(line, "COPY ")
	rest = strings.TrimSuffix(rest, " FROM stdin;")
	idx := strings.LastIndex(rest, " (")
	if idx < 0 {
		return ""
	}
	name := strings.TrimSpace(rest[:idx])
	return strings.ReplaceAll(name, `"`, "")
}
