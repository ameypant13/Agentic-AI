package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
)

const apiResourcesTTL = 10 * time.Minute

type apiResourceCache struct {
	mu   sync.Mutex
	byID map[string]cachedAPIResources
}

type cachedAPIResources struct {
	list []kube.APIResource
	at   time.Time
}

func (s *Server) registerResourceRoutes(rt *mux.Router) {
	rt.HandleFunc("/clusters/{id}/api-resources", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		s.apiRes.mu.Lock()
		c, ok := s.apiRes.byID[cl.Infra.ID]
		s.apiRes.mu.Unlock()
		if ok && time.Since(c.at) < apiResourcesTTL {
			return c.list, nil
		}
		list, err := cl.APIResources(ctx)
		if err != nil {
			return nil, err
		}
		s.apiRes.mu.Lock()
		s.apiRes.byID[cl.Infra.ID] = cachedAPIResources{list: list, at: time.Now()}
		s.apiRes.mu.Unlock()
		return list, nil
	})).Methods(http.MethodGet)

	rt.HandleFunc("/clusters/{id}/resources/{group}/{version}/{resource}", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		gvr, err := gvrFrom(r)
		if err != nil {
			return nil, err
		}
		return cl.List(ctx, gvr, r.URL.Query().Get("ns"))
	})).Methods(http.MethodGet)

	rt.HandleFunc("/clusters/{id}/resources/{group}/{version}/{resource}/{name}", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		gvr, err := gvrFrom(r)
		if err != nil {
			return nil, err
		}
		return cl.Get(ctx, gvr, r.URL.Query().Get("ns"), mux.Vars(r)["name"])
	})).Methods(http.MethodGet)

	rt.HandleFunc("/clusters/{id}/nodes/{name}", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return cl.NodeDetail(ctx, mux.Vars(r)["name"])
	})).Methods(http.MethodGet)

	rt.HandleFunc("/clusters/{id}/helm-releases", s.clusterRead(func(ctx context.Context, cl *kube.Cluster, r *http.Request) (any, error) {
		return nonNil(cl.HelmReleases(ctx, r.URL.Query().Get("ns")))
	})).Methods(http.MethodGet)
}

func gvrFrom(r *http.Request) (schema.GroupVersionResource, error) {
	v := mux.Vars(r)
	return kube.ParseKey(strings.Join([]string{v["group"], v["version"], v["resource"]}, "/"))
}
