# Provider subscription usage

The Provider list and detail share a cached subscription usage snapshot for Codex and supported
Kimi, GLM and Z.ai Coding Plan connections. The Codex editor uses the same Codex cache.
On entry they show cached data immediately, then request a fresh snapshot through the shared,
account-scoped request cache. An animated border and disabled refresh button indicate the request
is pending. A failed refresh retains the last snapshot and exposes the error. Replacing the bound
account invalidates the cached identity.

## Layout

Each Provider occupies a responsive card. Identity, routing status and primary actions form the
header; endpoint and credential details wrap below it. Model counts expand into the complete
upstream and logical model lists. Reset Cooldowns and Delete remain available in More.
A full-width subscription section sits below these details, with readable plan facts,
quota meters and the snapshot timestamp. Model-specific quotas expand separately. The details
and editor use the same quota renderer and keep expanded quota groups during refresh.

Reset credits use compact rows with the credit name, remaining validity, expiration and action.
The available count appears once beside the reset-credit heading. IDs, internal scope,
grant dates and upstream descriptions stay in the confirmation dialog; consumption is explained
there before submission. Provider pages use monochrome surfaces, fine dividers and unfilled
usage sections. Normal usage uses a neutral value and meter; high usage and imminent expiry
retain warning colors. Missing or partial credit details remain explicit.

## Display contract

| Returned data | Display |
| --- | --- |
| plan_type | Pro, Plus, Free, Go, Business (including team), Enterprise or Edu; preserve unknown plan names |
| primary_window / secondary_window | Render only present windows; determine label from limit_window_seconds, never from position or plan name |
| 604800 seconds | Weekly limit |
| 18000 seconds | 5-hour limit |
| Other positive duration | Actual day/hour/minute/second duration |
| Missing duration or percentage | Generic usage window / Not reported; never invent 0% or unlimited usage |
| additional_rate_limits | Separate named model groups with both returned windows |
| credits | Reported balance or explicit unlimited **credits**, without implying unlimited subscription usage |
| rate_limit_reset_credits.available_count | Available usage resets, including explicit zero; omit when absent |
| spend_control.individual_limit | Reported seat spending limit, used and remaining values; no currency inference |
| Limit or spending reached | Explicit status independent of numeric percentage |
| Reset time | Absolute reset_at, or observed_at + reset_after_seconds; never a stale countdown |

Window labels come from their reported durations, not the account plan or the position
of a field. A weekly primary window with no secondary window must remain weekly;
missing data does not establish an unlimited quota. Different model groups may
report different windows.

OpenAI's Codex pricing documentation describes varying account limits and separate Spark limits:
https://developers.openai.com/codex/pricing
The account snapshot is authoritative for the current window duration and reset time.

## Coding Plan usage

Usage is available for connections to supported official Coding Plan endpoints. Kimi uses the
configured API key or an existing native OAuth account bound to its registered endpoint.
GLM and Z.ai use the configured connection key. Refresh queries usage without sending an
inference request or changing scheduling and billing. Reset times are displayed when reported;
there is no reset action for these plans.

Kimi shows the reported 5-hour, weekly and monthly windows. Monthly Coding usage is a
breakdown of the monthly total. GLM and Z.ai keep credit amounts and MCP call counts separate
from usage percentages. Unreported quantities stay unavailable; a reported zero stays zero.
See [Kimi membership limits](https://www.kimi.com/en/help/membership/membership-update-rules) and
the [GLM/Z.ai usage plugin](https://github.com/zai-org/zai-coding-plugins/blob/0446d0bb0bc537d97d3ab3664c4b8b9c4a0e1254/plugins/glm-plan-usage/skills/usage-query-skill/scripts/query-usage.mjs).

## Verification

subscription-usage.test.mjs covers weekly-only Pro, dual-window Plus, independent Spark windows,
organizational credits and budgets, unknown plans, missing fields and HTML escaping.
quota-cache.test.mjs covers refresh deduplication, retained stale snapshots, navigation and
account replacement. coding-usage.test.mjs covers Coding Plan connection detection, rendering,
refresh and cache reuse. These checks are included in the console's npm test command.
