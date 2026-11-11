// Package metrics defines the hub's Prometheus metrics. All counters are
// registered with the default Prometheus registry on package init so that
// importing the package from any binary exposes /metrics consistently.
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// EventsConsumed counts the events read off the EVENTS stream.
	EventsConsumed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hub_events_consumed_total",
		Help: "Total events consumed from EVENTS stream.",
	})
	// TriggersMatched counts the times an event matched a trigger.
	TriggersMatched = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hub_triggers_matched_total",
		Help: "Total trigger matches.",
	})
	// EventsRouted counts the events handed to an agent.
	EventsRouted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hub_events_routed_total",
		Help: "Total events routed to agents.",
	})
	// RoutingErrors counts the events routing failed to deliver.
	RoutingErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hub_routing_errors_total",
		Help: "Total routing errors.",
	})
	// CronFires counts the cron trigger fires published to agents.
	CronFires = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hub_cron_fires_total",
		Help: "Total cron trigger fires published to agents.",
	})
)

func init() {
	prometheus.MustRegister(EventsConsumed, TriggersMatched, EventsRouted, RoutingErrors, CronFires)
}
