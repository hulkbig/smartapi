# Sessions and request explanations

[中文](session-observability.md)

Open **Sessions** below Requests to inspect conversation timelines, outcome and recovery statistics, usage, cost and latency. Select a request for a compact routing explanation and a link to full Request details. Providers overview and detail reuse the same component with multiple Session or Request sources.

## Grouping and statistics

Grouping uses a stable caller identity and high-confidence conversation evidence, such as a verified client thread or owned Responses lineage. Different callers remain separate; conflicting evidence does not merge Sessions. IP addresses, display names, prompt similarity and cache-affinity hints do not establish conversation identity. Requests without evidence remain independent, and old history is not retroactively grouped.

Statistics use the request arrival window `[from,to)` and all selected filters together. A logical request is counted once. Canceled and active requests are excluded from the success-rate denominator. Latency percentiles include sample counts. Request costs are not added to attempt costs again. Known zero, partial and unknown values remain distinct.

## Request and Response excerpts

**Request and response prefixes** in Settings is independent of full Request Debug capture. The default is metadata only. Enabling excerpts affects new requests and retains at most 160 Unicode characters each of meaningful visible input and successfully delivered output, plus at most three tool names.

Excerpts exclude system/developer content, hidden reasoning, tool arguments, image bodies and raw headers. Recognized credential formats are redacted, but visible text may still contain sensitive information. Turning capture off does not delete existing excerpts; they follow request-history retention. There is no content backfill.

Lists usually show the latest meaningful Request. Repeated input, including an identical truncated prefix, prefers an available Response. This comparison is independent of pagination and filters. Missing excerpts are omitted from lists, source cards and details instead of displaying repeated placeholders. Request status and delivery failures remain visible. Streaming excerpts contain only text delivered to the client.

## Candidates and playback

The diagram shows sources → SmartAPI → all Providers and connections in the selected request's recorded route. Steps without matching connections remain visible. Providers outside that route are not candidates.

Names and conditions are historical snapshots. Conditions can pass, fail, remain unknown or be unevaluated after short-circuiting. Execution updates append failures and recovery choices without re-probing upstreams or rewriting history using current health.

Selected connections remain distinct from calls in progress. Waiting for admission pulses only the gateway border in amber. A dispatched, unfinished call pulses the selected Provider and connection in blue; actual client output marks the delivery nodes. Upstream completion does not end a still-open client delivery. The first upstream protocol event does not prove visible client output. Recoverable failures return to SmartAPI before following the actual next connection. Cancellation, restart and delivery failure do not appear as successful delivery.

Views initially show the current or final state; completed requests do not autoplay. Explicit historical playback is labeled as replay and supports pause, stepping, speed and returning to the latest state. At 1×, steps remain visible for at least 1.6 seconds and transfer steps for at least 3 seconds. Each transfer takes about 2.8 seconds and runs once per newly observed event or replay step; polling and relayout do not restart it. Reduced motion preserves static highlighting and explanations. Hidden or offscreen views pause relevant animation or polling.

## Real requests in Model Routing

The Model Routing page visualizes the current logical model's conditional branches in evaluation order, keeps the default route visible, and shows each step's Provider, upstream model and conditions for advancing after a failure. A rule's `enabled` state describes its configuration; actual matches come from the selected request's recorded decisions and execution events.

The same page reuses the real request diagram above. Sources and requests are strictly filtered by the current logical model, with a time range, four sources per page, request selection and a link to full Request details. The same replay pace, working-node borders, static terminal states and reduced-motion behavior apply. Current configuration does not replace historical routing snapshots.

## Provider traffic and refresh

Provider traffic shows four source cards per page with pagination. A selected Request determines the highlighted route. Participation requires an actual attempt or dispatch; waiting sources are listed separately, and merely being a candidate does not count. The complete selected request's candidates remain visible, including other Providers.

Participating-request cost can include recovery at other Providers. Participation sets overlap, so their totals cannot be added into a global cost.

Views poll approximately every three seconds with one in-flight read per resource. Errors keep the last data visible with a notice; HTTP mode never substitutes mock data. Responses use a committed database snapshot. `as_of`, trace revision and event sequence identify observation progress; pagination does not freeze a changing dataset across multiple requests.

## Management API

Existing management authentication, CSRF and Settings write protection apply.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/sessions` | Scoped list, statistics, coverage and cursor |
| `GET /api/sessions/{id}` | Session latency, recovery and resources |
| `GET /api/sessions/{id}/requests` | Timeline using limit/offset |
| `GET /api/provider-traffic` | Session or Request sources |
| `GET /api/providers/{id}/traffic` | Participating and awaiting sources |
| `GET /api/requests/{id}` | Recorded candidates, conditions, updates and events |
| `GET/PATCH /api/settings` | `observability_content_mode=metadata_only\|excerpt` |

Collections default to 50 rows and allow at most 200. Filters include time bounds, caller, model, participating Provider, status, outcome and search. Search is limited to 200 characters and retained excerpts, safe metadata and IDs. An exact request-ID match takes priority. A single Session accepts `scope=all` for its retained history.
