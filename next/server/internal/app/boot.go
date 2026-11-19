package app

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/apikey"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/background"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/cluster"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/environment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event/delivery"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/group"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/job"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/notify"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/blobs"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/egress"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rollout"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/proxy"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/task"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usage"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/webui"
)

// boot is one core process being brought up: the dependencies each phase
// builds and the teardown stack they register. run threads the same value
// through the phases so they need no long parameter lists, and the shutdown
// order stays exactly the registration order reversed.
type boot struct {
	ctx     context.Context
	cfg     *config.Config
	version string
	log     *slog.Logger
	managed *managedCore

	stopper *teardown

	// storage and cluster
	db  *store.DB
	rdb redis.UniversalClient
	// sessRdb keeps the console sessions (tokenstore.NewClient).
	sessRdb redis.Cmdable
	cipher  *secret.Cipher
	cl      *cluster.Cluster

	// core services
	events  *event.Publisher
	reg     *registry.Registry
	pkgs    *registry.Packages
	bill    *billing.Service
	work    *background.Executor
	tasks   *task.Service
	settler *usage.Service
	az      *authz.Service
	idm     *iam.Service
	grp     *group.Service
	env     *environment.Service
	ntf     *notify.Service
	keys    *apikey.Service
	prx     *proxy.Service
	stats   *account.Stats
	acc     *account.Service

	converters   *convert.Registry
	limiter      *account.Limiter
	packageSrc   *blobs.Source
	marketClient *http.Client

	// how this node may work and coordinate (nil on a standalone node)
	canWork       func() bool
	canCoordinate func() bool

	// request path pieces the HTTP phase and the admission hooks share
	runtimeState

	// plugin runtime
	gw   *gateway.Gateway
	ctl  *rollout.Controller
	inst *install.Service
	mkt  *market.Service
	jobs *job.Bridge
	dlv  *delivery.Service
	// egress is the plugin tunnel and the plugin network access (CONTRACTS
	// §113), built before the account module (startEgress)
	egress  *egress.Provider
	schemas *dbschema.Manager

	// HTTP
	sharedAssets *webui.SharedAssets
	engine       *gin.Engine
	router       *httpapi.Router
}

// runtimeState is the part of the process whose lifecycle admission controls:
// the request gate, the builtin readiness flag and the one-shot background
// start. Keeping them together says which phase owns the transition.
type runtimeState struct {
	gate         *requestGate
	builtinReady *atomic.Bool
	startOnce    sync.Once
	startErr     error
}

// teardown is the shutdown stack: functions run in reverse registration
// order, none of them twice. A function registered again under the same key
// is moved to the end, which keeps "closing = inverse of the last successful
// phase" true when a dependency is deliberately re-registered later.
type teardown struct {
	fns  []func(context.Context)
	keys map[string]int
}

func newTeardown() *teardown { return &teardown{keys: map[string]int{}} }

// add registers fn under key, or moves an earlier registration of key to the
// end of the stack.
func (t *teardown) add(key string, fn func(context.Context)) {
	if idx, ok := t.keys[key]; ok {
		t.fns = append(t.fns[:idx], t.fns[idx+1:]...)
		for k, i := range t.keys {
			if i > idx {
				t.keys[k] = i - 1
			}
		}
	}
	t.keys[key] = len(t.fns)
	t.fns = append(t.fns, fn)
}

// run calls every teardown function in reverse registration order.
func (t *teardown) run(ctx context.Context) {
	for i := len(t.fns) - 1; i >= 0; i-- {
		t.fns[i](ctx)
	}
}
