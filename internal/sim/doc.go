// Package sim provides the network simulation core based on ns-x.
//
// This package wraps the bytedance/ns-x event-driven simulator and
// exposes a minimal Engine interface so that the upper layers (api,
// topology, protocol) can depend on abstractions rather than on the
// underlying library directly.
//
// On Linux hosts the optional gont-based emulator (see gont_emulator.go)
// can be used to forward real traffic through network namespaces; on
// other platforms the engine falls back to pure ns-x simulation.
package sim
