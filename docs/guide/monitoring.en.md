# Usage, speed and quotas

Console lists show common metrics directly. Hover or focus an icon for context; touch users can tap help icons.

## API key history

API Keys defaults to the last 7 days, with 24-hour and 30-day options. Sort by request count, tokens or last use, and filter by key status.

| Column | Meaning |
| --- | --- |
| Requests and trend | Finished logical requests in the selected window, counted once each |
| Token ↓ / ↑ | Recorded input / output tokens |
| Success rate | Successful share of finished requests in the selected window |
| Last used | Latest retained activity, which may fall outside the selected window |

Select a key name or chart button for the trend, model distribution and recent requests. Rotation keeps history under the same identity; revoked keys retain queryable history. Help icons describe coverage. Missing usage is not presented as known zero, and deleted history cannot be reconstructed.

## Model token/s

Models & Routing shows token/s for each logical model and beside every primary or fallback upstream route.

| Rate | Calculation |
| --- | --- |
| Logical model | Total output tokens divided by total elapsed time for successful requests, including waiting and recovery |
| Upstream model | Total output tokens divided by total elapsed time for successful upstream calls, grouped by Provider and actual model |

The rate is a duration-weighted average within the reporting window. It includes waiting and transport, so it is not pure decoding speed. Hover for the window and valid sample count. No valid samples displays `—`.

## Provider accounts and timing

Independent accounts have separate subrows. Protocol connections merge only when their shared quota is confirmed. Usage supports Codex and supported official Kimi, GLM and Z.ai Coding Plan endpoints; upstream reports determine usage and reset times.

| Display | Meaning |
| --- | --- |
| Reset | Countdown for that quota window; hover for the exact time |
| Blue Reset | At most 24 hours until reset |
| Green Reset | At most 1 hour until reset |
| Amber pending confirmation | The reported time has passed; fresh usage must confirm the reset |
| Next | Next model catalog synchronization |
| `—` | No confirmed upstream data |

Quota windows remain separate. Call counts, quota units and percentages are not combined. A failed refresh preserves the last snapshot and reports the error. Next becomes due when its time passes; paused synchronization is shown explicitly.

## Requests and sessions

Request execution history and details open by default. Session, Provider and model views retain request selection and scroll position during refresh. See [Sessions and request explanations](../session-observability.en.md) for event meanings.
