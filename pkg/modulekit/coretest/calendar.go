package coretest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// CalendarOccupancy reads the real service-only occupancy endpoint with an
// installed module's bearer. It includes all_day and exposes no event details.
func (c *Core) CalendarOccupancy(module string, from, to time.Time) []modulekit.CalendarOccupancyBlock {
	c.T.Helper()
	q := url.Values{"view": {"occupancy"}, "from_time": {from.Format(time.RFC3339)}, "to_time": {to.Format(time.RFC3339)}}
	r := c.ModuleClient(module).Must(http.StatusOK, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
	var result struct {
		Data []modulekit.CalendarOccupancyBlock `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Body), &result); err != nil {
		c.T.Fatal(err)
	}
	return result.Data
}
