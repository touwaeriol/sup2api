# moderation 0.11.2 screenshots

Playwright (chromium) against the web mock (`npx vite --mode mock`), code at feat/moderation-0.11.2 1e825aea49. zh locale.

| file | what |
|---|---|
| 01-desktop-jev-default-questions-shown | 1440: Jev auditor on the default set; questions shown directly, badge "使用默认 (content-audit-v3-zh)" |
| 02-desktop-view-default-questions | "查看默认题目" drawer (read-only, every row "与当前相同") |
| 03-desktop-edited-from-default | one question edited: badge "已基于默认修改" |
| 04-desktop-view-default-compare-edited | drawer marks the edited row "当前已修改" |
| 05-desktop-restore-confirm | "恢复为默认" confirm |
| 06-desktop-restored-preview-unsaved | restored, unsaved: defaults shown for preview, notice to save |
| 07-desktop-llm-default-prompt-shown | llm_verdict on built-in prompt/categories, shown directly |
| 08-desktop-llm-view-default | default prompt and categories drawer |
| 09-desktop-record-error-detail | record detail: failed outcome's redacted err_detail under the trace |
| 10-desktop-record-error-tooltip | the same in the verdict badge tooltip |
| 11-phone-jev-default-questions-shown | 390 phone: editor with default questions |
| 12-phone-view-default-questions | 390 phone: default questions drawer (full-screen page) |
| 13-phone-record-error-detail | 390 phone: record err_detail |
| 14-dark-jev-default-questions-shown | dark: editor |
| 15-dark-view-default-questions | dark: default questions drawer |

## Review fixes (feat/moderation-0.11.2 4eaf7c417f)

| file | what |
|---|---|
| 16-readonly-editor-view-default-enabled | read user (moderation:read only): editor read only, "查看默认题目" enabled, "恢复为默认" disabled |
| 17-readonly-view-default-questions | read user: default questions drawer, no restore button |
| 18-llm-questions-default-table | new llm_questions auditor: default table shown, thresholds hidden |
| 19-llm-questions-view-default-no-threshold-diff | its drawer: rows "与当前相同" (thresholds not compared) |

09/10/13 predate the fix: err_detail is now written without the "network: " prefix.
