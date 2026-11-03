package mozilla

import (
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// serverlist_snapshot.json is a copy of Mozilla's public server list
// (the same Remote Settings records the live endpoint serves), compiled
// into the app. It is the last-resort bootstrap path: on networks where
// every live path fails, the server page is still populated and a connect
// attempt is still possible. It goes stale as Mozilla rotates servers, so
// the live paths always run first when they can.

//go:embed serverlist_snapshot.json
var builtinSnapshot []byte

// BuiltinServerList parses the compiled server list.
func BuiltinServerList() ([]Country, error) {
	var out struct {
		Data []struct {
			Country *Country `json:"country"`
			Country2
		} `json:"data"`
	}
	if err := json.Unmarshal(builtinSnapshot, &out); err != nil {
		return nil, err
	}
	var countries []Country
	for _, rec := range out.Data {
		c := rec.Country
		if c == nil {
			c = &Country{Name: rec.Name, Code: rec.Code, Cities: rec.Cities}
		}
		if c.Code == "" || len(c.Cities) == 0 {
			continue
		}
		c.Code = strings.ToUpper(c.Code)
		countries = append(countries, *c)
	}
	if len(countries) == 0 {
		return nil, errors.New("builtin server list is empty")
	}
	sort.Slice(countries, func(i, j int) bool { return countries[i].Name < countries[j].Name })
	return countries, nil
}
