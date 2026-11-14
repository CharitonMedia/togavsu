package billing

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"sort"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// WriteJSON writes r exactly as json.Encoder would (keys of each row sorted, HTML
// characters escaped, a newline after), without reflecting over every cell: the unbilled
// report at the AC-017 seed is about 100,000 rows. Values of other types fall back to
// encoding/json.
func (r Report) WriteJSON(w io.Writer) error {
	b := bufio.NewWriterSize(w, 64<<10)
	buf := make([]byte, 0, 1024)
	buf = append(buf, `{"report":`...)
	buf = appendJSONString(buf, r.Report)
	buf = append(buf, `,"columns":`...)
	if r.Columns == nil {
		buf = append(buf, "null"...)
	} else {
		buf = append(buf, '[')
		for i, c := range r.Columns {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendJSONString(buf, c)
		}
		buf = append(buf, ']')
	}
	buf = append(buf, `,"rows":`...)
	if r.Rows == nil {
		buf = append(buf, "null"...)
	} else {
		buf = append(buf, '[')
	}
	var keys []string
	for i, row := range r.Rows {
		if i > 0 {
			buf = append(buf, ',')
		}
		if row == nil {
			buf = append(buf, "null"...)
			continue
		}
		// Report rows usually share one key set: reuse the previous row's sorted keys
		// when this row has exactly those keys.
		same := len(keys) == len(row)
		for _, k := range keys {
			if !same {
				break
			}
			_, same = row[k]
		}
		if !same {
			keys = keys[:0]
			for k := range row {
				keys = append(keys, k)
			}
			sort.Strings(keys)
		}
		buf = append(buf, '{')
		for j, k := range keys {
			if j > 0 {
				buf = append(buf, ',')
			}
			buf = appendJSONString(buf, k)
			buf = append(buf, ':')
			var err error
			if buf, err = appendJSONValue(buf, row[k]); err != nil {
				return err
			}
		}
		buf = append(buf, '}')
		if len(buf) > 32<<10 {
			if _, err := b.Write(buf); err != nil {
				return err
			}
			buf = buf[:0]
		}
	}
	if r.Rows != nil {
		buf = append(buf, ']')
	}
	buf = append(buf, "}\n"...)
	if _, err := b.Write(buf); err != nil {
		return err
	}
	return b.Flush()
}

func appendJSONValue(buf []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case string:
		return appendJSONString(buf, x), nil
	case domain.ID:
		return appendJSONString(buf, string(x)), nil
	case int64:
		return strconv.AppendInt(buf, x, 10), nil
	case int:
		return strconv.AppendInt(buf, int64(x), 10), nil
	case domain.Cents:
		return strconv.AppendInt(buf, int64(x), 10), nil
	case bool:
		return strconv.AppendBool(buf, x), nil
	case nil:
		return append(buf, "null"...), nil
	}
	enc, err := json.Marshal(v)
	return append(buf, enc...), err
}

// jsonPlain marks the bytes encoding/json copies into a string unchanged: printable
// ASCII except the quote, the backslash and the HTML characters it escapes.
var jsonPlain = func() (t [256]bool) {
	for c := ' '; c < '\x7f'; c++ {
		t[c] = c != '"' && c != '\\' && c != '<' && c != '>' && c != '&'
	}
	return t
}()

// appendJSONString quotes s as encoding/json does; plain printable ASCII is copied, and
// anything else goes through encoding/json so escapes match. It tests eight bytes at a
// time: report rows are mostly ids, dates and short descriptions.
func appendJSONString(buf []byte, s string) []byte {
	i := 0
	for ; i+8 <= len(s); i += 8 {
		if !plain8(binary.LittleEndian.Uint64([]byte(s[i : i+8]))) {
			return appendQuoted(buf, s)
		}
	}
	for ; i < len(s); i++ {
		if !jsonPlain[s[i]] {
			return appendQuoted(buf, s)
		}
	}
	buf = append(buf, '"')
	buf = append(buf, s...)
	return append(buf, '"')
}

func appendQuoted(buf []byte, s string) []byte {
	enc, _ := json.Marshal(s)
	return append(buf, enc...)
}

// plain8 reports whether all eight bytes of x are jsonPlain: none has the high bit, none
// is below a space, and none is DEL or one of the five characters encoding/json escapes.
// A byte equal to c shows as a zero byte of x ^ (c in every byte).
func plain8(x uint64) bool {
	const ones, highs = 0x0101010101010101, 0x8080808080808080
	zero := func(v uint64) bool { return (v-ones)&^v&highs != 0 }
	if x&highs != 0 || (x-ones*0x20)&^x&highs != 0 {
		return false
	}
	for _, c := range []uint64{'"', '\\', '<', '>', '&', 0x7f} {
		if zero(x ^ ones*c) {
			return false
		}
	}
	return true
}
