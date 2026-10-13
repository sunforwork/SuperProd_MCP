# Model reference

Compatibility: upstream `71eb7780bcf5d6b1dfdcd39a8a8265547d040760`, schema 4.

`task`, `project`, `tag`, `note`, `metric`, `simpleCounter`, `taskRepeatCfg`, `issueProvider`, `section` use `ids` + `entities`. Preserve array order, unknown fields and absent/null distinction.

`task.timeSpentOnDay` maps YYYY-MM-DD to milliseconds; `timeSpent` is its total. A parent aggregates its children. Parent `timeEstimate` is the sum of unfinished children's positive remaining estimates. Updating a child day's time uses the original client's incremental parent-time algorithm and does not silently repair prior inconsistencies. Task `notes` is text. TODAY is a virtual tag, never an ordinary `tagIds` entry. `dueDay`, `dueWithTime`, `doneOn`, `deadlineDay`, and `deadlineWithTime` have distinct semantics. Completion sets doneOn, reopening clears it; planning/deadline commands and recurring completion are pending. Literal task creation uses the pinned defaults and explicit PROJECT/TAG context. Inbox ID is `INBOX_PROJECT`.

`note` has projectId (nullable), content, created, modified, isPinnedToToday, optional imgUrl/isLock/backgroundColor. Client note updates do not automatically bump `modified`. Project noteIds and note.todayOrder must follow note lifecycle.

`task_plan_today` mirrors the ID-based client shortcut through HPT. Its payload captures the logical day, day offset and parent map. It removes affected planner-day references, preserves a valid time already on the same logical day, and clears other-day or invalid scheduled times when the client's update path runs. It also preserves the original no-op path, including any stale planner references on that path. General planning dates, timed scheduling and recurrence remain pending.

`planner.days`, `boards.boardCfgs`, `menuTree`, `globalConfig`, `timeTracking`, raw `pluginUserData`, `pluginMetadata`, `reminders`, and the two archive partitions must be retained even when an operation does not use them.

`timeTracking.project/PROJECT_ID/DATE` and `timeTracking.tag/TAG_ID/DATE` hold s/e start/end timestamps, b break count and bt break duration. `time_update_work_context` maps to KW and preserves the special entityChanges payload. Ordinary manual task time maps to HU, not a synthetic timer delta.

Project creation uses pinned defaults. Unarchive/reopen clears isDone and sets doneOn to null. Disabling backlog emits PU then PBA. Tag creation also appends a menu-tree entry; currently it requires explicit color. Only title/icon/color and the documented project boolean fields are writable through ordinary updates. Reordering uses the client's handling of Inbox, archived and hidden projects. Deletion, completion-resolution workflows and advanced configuration commands are pending.

Free editing in v2.2 addresses the complete business JSON, including `/archiveYoung` and `/archiveOld`. `read_business_json` reads JSON Pointers with pagination. `begin_free_edit` and `apply_free_edit` support add/remove/replace/move/copy/test without command mapping or a per-field whitelist. Intermediate drafts may be inconsistent. `propose_free_edit` validates the final structure, entity indexes and client relationships, then publishes an immutable approval preview. A person approves the complete native snapshot replacement once. Raw edits do not run normal command effects: update intended references and aggregates explicitly. Protocol clocks and service credentials are server-owned. The five per-device sync settings (syncInterval, isManualSyncOnly, syncProvider, isEnabled, isEncryptionEnabled) cannot be changed through shared JSON. Redaction placeholders cannot be written back.

On version conflict, tell the user, read the latest cloud document and restart editing and approval; never automatically merge a stale draft. Named backup tools reference existing encrypted history without duplicating its content. Deleting a backup removes its named recovery entry, not underlying sync history. When confident about the goal, meaning and result, execute without a named backup; create one only when explicitly requested or a recovery point is needed.

Inspect returns raw persisted data and redacts configuration secrets. It does not claim full client selector or workflow parity. Full action source inventory: `resource://super-productivity/actions`.
