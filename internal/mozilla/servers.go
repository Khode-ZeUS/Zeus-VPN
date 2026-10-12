package mozilla

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
)

const serverListURL = "https://firefox.settings.services.mozilla.com/v1/buckets/main/collections/vpn-serverlist/records"

// RecommendedCode is Firefox's pseudo-country for the auto-selected server.
const RecommendedCode = "REC"

type Protocol struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type Server struct {
	Hostname    string     `json:"hostname"`
	Port        int        `json:"port"`
	Quarantined bool       `json:"quarantined"`
	Protocols   []Protocol `json:"protocols"`
}

type City struct {
	Name    string   `json:"name"`
	Code    string   `json:"code"`
	Servers []Server `json:"servers"`
}

type Country struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Cities []City `json:"cities"`
}

// Endpoint is a concrete upstream proxy to tunnel through.
type Endpoint struct {
	Host        string
	Port        int
	CountryCode string
	CountryName string
	CityName    string
}

func (e Endpoint) Addr() string { return fmt.Sprintf("%s:%d", e.Host, e.Port) }

// connectTarget returns the HTTP CONNECT endpoint of a server, if it has one.
func (s Server) connectTarget() (string, int, bool) {
	if len(s.Protocols) == 0 {
		return s.Hostname, s.Port, true
	}
	for _, p := range s.Protocols {
		if p.Name == "connect" {
			host, port := p.Host, p.Port
			if host == "" {
				host = s.Hostname
			}
			if port == 0 {
				port = s.Port
			}
			return host, port, true
		}
	}
	return "", 0, false
}

func FetchServerList() ([]Country, error) {
	req, _ := http.NewRequest("GET", serverListURL, nil)
	applyHeaders(req.Header)
	resp, err := Client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not download server list: %w", err)
	}
	defer resp.Body.Close()
	body := readBody(resp, 32<<20)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server list: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			Country *Country `json:"country"`
			Country2
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("bad server list: %w", err)
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
	sort.Slice(countries, func(i, j int) bool { return countries[i].Name < countries[j].Name })
	return countries, nil
}

// Country2 lets a record carry the country fields at the top level.
type Country2 struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Cities []City `json:"cities"`
}

// Candidates lists usable endpoints for a country, optionally narrowed to one city.
func Candidates(countries []Country, countryCode, cityCode string) []Endpoint {
	var out []Endpoint
	for _, c := range countries {
		if !strings.EqualFold(c.Code, countryCode) {
			continue
		}
		for _, city := range c.Cities {
			if cityCode != "" && !strings.EqualFold(city.Code, cityCode) {
				continue
			}
			for _, s := range city.Servers {
				if s.Quarantined {
					continue
				}
				if host, port, ok := s.connectTarget(); ok {
					out = append(out, Endpoint{Host: host, Port: port, CountryCode: c.Code, CountryName: c.Name, CityName: city.Name})
				}
			}
		}
	}
	return out
}

func PickEndpoint(countries []Country, countryCode, cityCode string) (Endpoint, error) {
	if countryCode == "" {
		countryCode = RecommendedCode
	}
	cands := Candidates(countries, countryCode, cityCode)
	if len(cands) == 0 && cityCode != "" {
		cands = Candidates(countries, countryCode, "")
	}
	if len(cands) == 0 {
		return Endpoint{}, fmt.Errorf("no available servers for %s", countryCode)
	}
	return cands[rand.IntN(len(cands))], nil
}
