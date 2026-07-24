package sim

import (
	"os"
	"os/exec"
	"runtime"

	"ensp-lab/internal/logging"
	"ensp-lab/internal/topology"

	"go.uber.org/zap"
)

func canRunGont() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Geteuid() != 0 {
		logging.Warn("gont requires root privileges",
			zap.String("requirement", "CAP_NET_ADMIN"),
			zap.Int("current_uid", os.Geteuid()),
		)
		return false
	}
	_, err := exec.LookPath("ovs-vsctl")
	if err != nil {
		logging.Warn("gont requires Open vSwitch",
			zap.String("tool", "ovs-vsctl"),
			zap.Error(err),
		)
		return false
	}
	return true
}

func NewEngine(topo *topology.Topology) (Engine, error) {
	if runtime.GOOS == "linux" && canRunGont() {
		eng, err := NewGontEngine(topo)
		if err == nil {
			logging.Info("engine mode=gont",
				zap.String("platform", runtime.GOOS),
				zap.String("description", "real namespace traffic"),
			)
			return eng, nil
		}
		logging.Warn("gont initialization failed, falling back to ns-x", zap.Error(err))
	} else {
		if runtime.GOOS == "linux" {
			logging.Info("running in simulation-only mode",
				zap.String("reason", "gont unavailable due to permissions or missing OVS"),
			)
		} else {
			logging.Info("running in simulation-only mode",
				zap.String("reason", "gont unavailable on non-Linux platform"),
				zap.String("platform", runtime.GOOS),
			)
		}
	}
	eng, err := NewNSxEngine(topo)
	if err != nil {
		return nil, err
	}
	logging.Info("engine mode=ns-x",
		zap.String("description", "event-driven simulation"),
	)
	return eng, nil
}
