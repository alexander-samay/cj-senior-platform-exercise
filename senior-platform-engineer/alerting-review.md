# Alerting review

## Route trace

Every alert starts at the root route. The root matches all alerts and supplies `default-slack` only as the fallback receiver. Alertmanager then checks the child routes in their configured order.

`KubePersistentVolumeFillingUp` has `namespace="platform"`, so it matches the first child route's regular expression, `namespace =~ "platform|monitoring"`. That route selects `platform-slack`. Its `continue` value is omitted, which means the default is `false`. Alertmanager therefore stops checking sibling routes after this first match. It never evaluates the later `severity="critical"` child, even though the alert also has that label. The notification goes to `platform-slack`, not `pagerduty-critical`.

The root's `group_by` settings are inherited because the platform route does not replace them. `group_interval: 5m` controls subsequent notifications for the group; it does not cause the alert to fall through to PagerDuty.

## Independent inhibition risk

The inhibition rule can suppress the notification independently of route selection. If a firing `ClusterUpgradeInProgress` alert has `cluster="prod-us-east-1"`, it is a matching source alert. Every target alert with `severity="critical"` and the same `cluster` value is inhibited, including an unrelated persistent-volume emergency. In that case Alertmanager still computes a receiver, but it does not send the inhibited notification.

This rule is much broader than the likely intent. It says that one cluster-upgrade signal may silence every critical condition in the cluster, not only known upgrade-related noise. It also depends on the source alert resolving correctly; a stale upgrade alert can extend the suppression.

## Changes

I would make critical paging explicit and order-independent for the intended duplicate delivery:

```yaml
route:
  receiver: default-slack
  group_by: [alertname, cluster]
  routes:
    - matchers:
        - severity = "critical"
      receiver: pagerduty-critical
      group_wait: 30s
      continue: true

    - matchers:
        - namespace =~ "platform|monitoring"
      receiver: platform-slack
      group_interval: 5m
```

Critical alerts now page first. `continue: true` deliberately allows a platform or monitoring alert to reach the following Slack route as well. Non-critical platform alerts still go only to `platform-slack`, and unmatched alerts retain the root fallback. If the desired policy is PagerDuty only for critical alerts, I would remove `continue` instead of assuming that duplicate Slack delivery is wanted.

I would remove the blanket inhibition rule or narrow its targets to an explicit list of alerts that an upgrade is known to trigger harmlessly. I would not use `severity="critical"` as the target selector. Before shipping, I would ask the cluster-upgrade owner which exact alert names are expected, verify that the source alert always resolves, and decide whether storage, control-plane, and data-protection alerts must be exempt under all circumstances.

I would validate the result in CI with Alertmanager's configuration checker and table-driven route tests containing at least:

- critical platform alert -> PagerDuty and platform Slack;
- warning platform alert -> platform Slack only;
- critical non-platform alert -> PagerDuty;
- unmatched warning -> default Slack;
- active upgrade plus each deliberately inhibitable target;
- active upgrade plus storage-full alert -> still pages.

I also ran the proposed route tree through `amtool` while preparing this answer. `amtool check-config` accepted it, and route tests resolved a critical platform alert to `pagerduty-critical,platform-slack`, a warning platform alert to `platform-slack`, a critical non-platform alert to `pagerduty-critical`, and an unmatched warning to `default-slack`. The original tree resolved the incident's label set only to `platform-slack`.

Before rollout I would also verify the PagerDuty routing key and service state with a controlled test alert, confirm Slack and PagerDuty grouping expectations, check for active silences or mute intervals outside this snippet, and inspect Alertmanager notification metrics and logs. A correct route does not prove that the downstream integration is usable.

## Why review missed it

Each child looks reasonable in isolation: platform alerts have a platform channel and critical alerts have PagerDuty. The failure is in the interaction between ordered sibling routes and the implicit `continue: false` default. YAML indentation makes the two entries look like independent rules, while Alertmanager treats them as an ordered route tree. The broad inhibition rule is separated from the route block, so its effect on critical paging is also easy to review independently and underestimate. A syntax-only review confirms that the file is valid but cannot confirm that representative label sets reach the intended receivers. Route-behavior tests would have made both mistakes visible.

Reference: [Alertmanager configuration and route traversal](https://prometheus.io/docs/alerting/latest/configuration/).
