# Software Engineer 2 - Platform — Take-Home Assignment

## Overview

Four things are wrong in a production environment. You get what an on-call engineer would
actually get: the symptoms, and nothing else.

We are not asking you to fix them. We are asking **what you think is going on, and what you
would look at next.** Short answers are correct answers here — a paragraph per case is plenty.

You will walk through your reasoning live in a later conversation, so write down what you
actually think rather than what sounds thorough.

---

## AI Policy

AI tools are allowed and expected. If you use AI, include `ai-usage.md` with:

1. Which tool you used
2. Prompt summaries or prompts
3. Suggestions you accepted
4. Suggestions you rejected
5. What you independently verified

We are not evaluating whether you used AI. We are evaluating whether you used it responsibly —
and you will be asked to talk through your reasoning without it later.

---

## Case 1 — A service is down

An alert fires at 2:40 AM:

```
[PagerDuty] FIRING: checkout-api — 0 ready replicas (threshold: 1)
Namespace: checkout
Cluster: prod-us-east-1
```

You have cluster access. Nobody deployed anything in the last four days.

```
$ kubectl get pods -n checkout
NAME                            READY   STATUS             RESTARTS      AGE
checkout-api-7d9f4b8c6-xk2mq    0/1     CrashLoopBackOff   14 (2m ago)   4d
checkout-api-7d9f4b8c6-p8vnl    1/1     Running            0             4d
```

Answer:

- What are the possible explanations, and which do you think is most likely?
- What is the first command you run, and what are you hoping it tells you?
- One replica is healthy and one is not, on the same image and the same spec. What does that
  tell you?

---

## Case 2 — Pods that will not schedule

A team says their deployment has been stuck for twenty minutes. Nothing is crashing — the pods
have never started.

```
$ kubectl get pods -n reporting
NAME                          READY   STATUS    RESTARTS   AGE
reporting-worker-5f8d-2xk4n   0/1     Pending   0          21m
reporting-worker-5f8d-9wqp7   0/1     Pending   0          21m
reporting-worker-5f8d-lm3zt   0/1     Pending   0          21m

$ kubectl describe pod reporting-worker-5f8d-2xk4n -n reporting
...
Events:
  Type     Reason             Message
  ----     ------             -------
  Warning  FailedScheduling   0/9 nodes are available: 3 Insufficient cpu,
                              4 node(s) had untolerated taint {workload: batch},
                              2 Insufficient memory
```

Answer:

- Read that event back in your own words. What is it telling you about each group of nodes?
- What would you check to decide whether this is the team's problem or the platform's problem?
- What are two different ways this gets resolved, and what does each one cost?

---

## Case 3 — A network path that does not work

A service in one VPC cannot reach a database in another. Someone has already set up VPC
peering between the two, and the peering connection shows as `active`.

Connections time out. They do not get refused.

Answer:

- List everything that has to be true for this traffic to arrive. You are welcome to be
  boring and complete.
- Where would you look first, and why that first?
- The connection times out rather than being refused. Does that narrow anything down for you?

---

## Case 4 — An alert that did not fire

A batch job stopped running for two days. Nobody noticed until a downstream team asked why
their report was empty. This alert was in place the whole time and never fired:

```yaml
groups:
  - name: batch
    rules:
      - alert: BatchJobFailing
        expr: batch_job_last_run_success == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Batch job {{ $labels.job_name }} is failing"
```

The metric `batch_job_last_run_success` is pushed by the job itself when it finishes: `1` on
success, `0` on failure.

Answer:

- The job stopped running entirely. Walk through what this alert evaluates to in that
  situation.
- Why did nobody get paged?
- What would you change so this case is covered? You do not need to write working PromQL —
  describe what the alert should be watching.

---

## Deliverable

One file, `diagnosis.md`, with your answers to the four cases. Plus `ai-usage.md` if you used
AI.

If you get stuck on a mechanism, say so and describe how you would find out. "I don't know how
X works, here's how I'd figure it out" is a real answer and we would rather read it than a
confident guess.

## What We're Assessing

- How you reason from a symptom toward a cause
- Whether you form hypotheses before reaching for commands
- Whether you can tell what you know from what you are assuming
- How clearly you explain your thinking to someone else
