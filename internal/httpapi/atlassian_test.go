package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ismailperim/briefd/internal/atlassian"
	"github.com/ismailperim/briefd/internal/httpapi"
)

func TestAtlassianHealthEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		health func(context.Context) []atlassian.Health
		want   int
	}{
		{"none configured", nil, 0},
		{"jira", func(context.Context) []atlassian.Health {
			return []atlassian.Health{{Product: "Jira", Status: "ok", Reachable: true}}
		}, 1},
	} {
		srv := httptest.NewServer(httpapi.New(httpapi.Deps{APIToken: "tok", AtlassianHealth: tc.health, Version: "test"}))
		resp, err := http.Get(srv.URL + "/api/atlassian/health")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: without token = %d", tc.name, resp.StatusCode)
		}
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/atlassian/health", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Products []atlassian.Health `json:"products"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body.Products) != tc.want {
			t.Errorf("%s: %d %+v", tc.name, resp.StatusCode, body)
		}
		srv.Close()
	}
}
