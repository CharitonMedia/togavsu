package sample

// Positive fixture: empty checks, a loop counter, a zero error result, and a zero conversion.

func empty(items []int) bool { return len(items) == 0 || len(items) > 0 || cap(items) != 0 }

func nonempty(items []int) bool { return len(items) >= 1 || len(items) < 1 }

func loop(items []int) {
	for i := 0; i < len(items); i++ {
		_ = i
	}
}

func missing() (int, error) { return 0, errSample }

var errSample = errType{}

type errType struct{}

func (errType) Error() string { return "sample" }

func zero() int64 { return int64(0) }

func first(s []byte) byte { return s[0] }
