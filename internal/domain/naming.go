package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrPattern is returned when a Doc Tender naming pattern is rejected.
var ErrPattern = errors.New("invalid naming pattern")

// Naming tokens the admin may use. A status token is refused.
const (
	TokenClientLast   = "<clientlast>"
	TokenClientFirst  = "<clientfirst>"
	TokenMatterNumber = "<matter#>"
	TokenMatterName   = "<mattername>"
	TokenWhat         = "<what-it-is>"
	TokenDocDate      = "<doc-date>"
	TokenReceivedDate = "<received-date>"
	TokenCategory     = "<category>"
	TokenExt          = "<ext>"
)

var namingTokens = map[string]bool{
	TokenClientLast:   true,
	TokenClientFirst:  true,
	TokenMatterNumber: true,
	TokenMatterName:   true,
	TokenWhat:         true,
	TokenDocDate:      true,
	TokenReceivedDate: true,
	TokenCategory:     true,
	TokenExt:          true,
}

var tokenRE = regexp.MustCompile(`<[^>]+>`)

// ValidateNamingPattern accepts the Doc Tender pattern language and refuses any status token.
func ValidateNamingPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("%w: empty", ErrPattern)
	}
	if !strings.Contains(pattern, TokenExt) {
		return fmt.Errorf("%w: needs %s", ErrPattern, TokenExt)
	}
	found := tokenRE.FindAllString(pattern, -1)
	if len(found) == 0 {
		return fmt.Errorf("%w: needs tokens", ErrPattern)
	}
	for _, tok := range found {
		if strings.Contains(strings.ToLower(tok), "status") {
			return fmt.Errorf("%w: status token is refused", ErrPattern)
		}
		if !namingTokens[tok] {
			return fmt.Errorf("%w: unknown token %s", ErrPattern, tok)
		}
	}
	return nil
}

// DefaultCasePattern matches a Missouri docket number such as 23AR-CV00825.
const DefaultCasePattern = `^\d{2}[A-Z]{2}-[A-Z]{2}\d+$`

// MatchCasePattern reports whether value matches one pattern.
func MatchCasePattern(pattern, value string) (bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(value), nil
}
