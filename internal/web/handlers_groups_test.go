package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGroupsCollectionGET(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Profile:    "test",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{
			Profile: "test",
			Items: []MenuItem{
				{
					Type: MenuItemTypeGroup,
					Group: &MenuGroup{
						Name: "work",
						Path: "work",
					},
				},
				{
					Type: MenuItemTypeSession,
					Session: &MenuSession{
						ID:    "sess-1",
						Title: "alpha",
					},
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"groups"`) {
		t.Errorf("expected 'groups' key in response, got: %s", body)
	}
	if !strings.Contains(body, `"work"`) {
		t.Errorf("expected group name in response, got: %s", body)
	}
}

func TestGroupsCollectionPOSTCreatesGroup(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.mutator = &fakeMutator{
		createGroupFn: func(name, parentPath string) (string, error) {
			return "new-group", nil
		},
	}

	body := strings.NewReader(`{"name":"newgroup"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/groups", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "new-group") {
		t.Errorf("expected group path in response, got: %s", rr.Body.String())
	}
}

func TestGroupCreateMissingName(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.mutator = &fakeMutator{}

	body := strings.NewReader(`{"parentPath":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/groups", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ErrCodeBadRequest) {
		t.Errorf("expected INVALID_REQUEST error, got: %s", rr.Body.String())
	}
}

func TestGroupRenamePATCHOK(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.mutator = &fakeMutator{
		renameGroupFn: func(groupPath, newName string) error { return nil },
	}

	body := strings.NewReader(`{"name":"renamed"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/groups/mygroup", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
}

func TestGroupDeleteOK(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.mutator = &fakeMutator{
		deleteGroupFn: func(groupPath string) error { return nil },
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/groups/mygroup", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
}

func TestGroupChangeOK(t *testing.T) {
	var gotGroup, gotDest string
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.mutator = &fakeMutator{
		reparentGroupFn: func(groupPath, destParentPath string) (string, error) {
			gotGroup = groupPath
			gotDest = destParentPath
			return "platform/backend", nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/groups/ops/backend/change", strings.NewReader(`{"destParentPath":"platform"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if gotGroup != "ops/backend" || gotDest != "platform" {
		t.Fatalf("reparent call = %q -> %q", gotGroup, gotDest)
	}
	if !strings.Contains(rr.Body.String(), "platform/backend") {
		t.Fatalf("response missing new group path: %s", rr.Body.String())
	}
}

func TestGroupReorderOK(t *testing.T) {
	var gotGroup, gotDirection string
	var gotPosition *int
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.mutator = &fakeMutator{
		reorderGroupFn: func(groupPath, direction string, position *int) (int, int, error) {
			gotGroup = groupPath
			gotDirection = direction
			gotPosition = position
			return 2, 1, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/groups/platform/backend/reorder", strings.NewReader(`{"direction":"up"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if gotGroup != "platform/backend" || gotDirection != "up" || gotPosition != nil {
		t.Fatalf("reorder call = group %q direction %q position %v", gotGroup, gotDirection, gotPosition)
	}
	if !strings.Contains(rr.Body.String(), `"fromPosition":2`) || !strings.Contains(rr.Body.String(), `"toPosition":1`) {
		t.Fatalf("response missing positions: %s", rr.Body.String())
	}
}

func TestGroupDeleteDefaultGroupReturns400(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		WebMutations: true,
	})
	srv.menuData = &fakeMenuDataLoader{snapshot: &MenuSnapshot{}}
	srv.mutator = &fakeMutator{}

	req := httptest.NewRequest(http.MethodDelete, "/api/groups/my-sessions", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cannot delete default group") {
		t.Errorf("expected default group protection message, got: %s", rr.Body.String())
	}
}
