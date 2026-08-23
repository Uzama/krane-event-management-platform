# ER diagram — data model

Feature 01 deliverable; the data-model half of the required design diagram. Attribute rationale lives in [`requirements.md`](./requirements.md) §2. Refreshed by item 27 to match what shipped.

Nine tables. Columns whose owning feature is later than item 02 are drawn now and tagged with that item, so item 27's refresh is a text edit rather than a redraw.

```mermaid
erDiagram
    users {
        uuid        id           PK "uuidv7()"
        text        subject      UK "OIDC sub claim"
        citext      email        UK "never exposed to attendees"
        text        name
        timestamptz created_at
        timestamptz updated_at
    }

    events {
        uuid        id           PK "uuidv7()"
        text        name
        text        description  "nullable"
        text        timezone     "IANA name, never an offset"
        timestamptz starts_at
        timestamptz ends_at
        integer     version      "item 17"
        timestamptz deleted_at   "nullable, soft delete"
        timestamptz created_at
        timestamptz updated_at
    }

    event_members {
        uuid        id           PK "uuidv7()"
        uuid        event_id     FK
        uuid        user_id      FK
        text        role         "admin | contributor | attendee"
        timestamptz created_at
        timestamptz updated_at
    }

    role_permissions {
        text        role         PK
        text        resource     PK "event | member | room | session | invitation"
        text        action       PK "create | read | update | delete | assign-role"
    }

    rooms {
        uuid        id           PK "uuidv7()"
        uuid        event_id     FK "rooms are per-event - decision D8"
        text        name         "unique within event"
        integer     capacity     "nullable"
        integer     version      "item 17"
        timestamptz created_at
        timestamptz updated_at
    }

    sessions {
        uuid        id           PK "uuidv7()"
        uuid        event_id     FK
        uuid        room_id      FK "EXCLUDE with time_range - item 16"
        uuid        speaker_id   FK "to users, any user - EXCLUDE with time_range"
        text        title
        text        description  "nullable - Optional[T] proof case, item 20"
        tstzrange   time_range   "one range, not two columns"
        integer     version      "item 17"
        timestamptz deleted_at   "nullable, soft delete"
        timestamptz created_at
        timestamptz updated_at
    }

    invitations {
        uuid        id           PK "uuidv7()"
        uuid        event_id     FK
        uuid        user_id      FK "NULLABLE - invitee may not be a user yet"
        citext      email        "always present"
        text        role         "role being offered"
        timestamptz created_at
        timestamptz updated_at
    }

    audit_log {
        uuid        id           PK "uuidv7()"
        uuid        actor_id     FK "who - from the authenticated sub"
        text        entity_type
        uuid        entity_id    "no FK - history outlives its subject"
        text        action
        jsonb       before       "RETURNING OLD"
        jsonb       after        "RETURNING NEW"
        text        request_id
        timestamptz created_at
    }

    idempotency_keys {
        uuid        id           PK "uuidv7()"
        uuid        actor_id     FK "keys scoped per actor"
        text        key          "client Idempotency-Key header"
        text        endpoint
        text        request_hash "same key, different body -> 422"
        integer     response_status
        jsonb       response_body
        timestamptz created_at
    }

    events           ||--o{ event_members    : "has roster"
    users            ||--o{ event_members    : "holds per-event role"
    events           ||--o{ rooms            : "owns"
    events           ||--o{ sessions         : "schedules"
    rooms            ||--o{ sessions         : "hosts"
    users            ||--o{ sessions         : "speaks at"
    events           ||--o{ invitations      : "invites to"
    users            |o--o{ invitations      : "may be invitee"
    users            ||--o{ audit_log        : "acted"
    users            ||--o{ idempotency_keys : "retried"
```

`role_permissions` stands alone: it is keyed by the `role` *string*, not by a FK to `event_members`. That is deliberate — the chokepoint loads the whole table once and answers `can(user, action, resource)` from memory, and adding a role stays an INSERT.

## Uniqueness and exclusion constraints

The constraints carry the correctness guarantees; the columns above only make them expressible.

| table | constraint | purpose | item |
|---|---|---|---|
| `users` | `unique (subject)` | one row per OIDC identity | 06 |
| `users` | `unique (email)` | | 02 |
| `event_members` | `unique (event_id, user_id)` | one role per person per event | 09 |
| `rooms` | `unique (event_id, name)` | | 11 |
| `sessions` | `EXCLUDE USING gist (room_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL)` | **no room double-booking** | 16 |
| `sessions` | `EXCLUDE USING gist (speaker_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL)` | **no speaker double-booking** | 16 |
| `invitations` | `unique (event_id, email)` | row-level invite dedup, independent of header replay | 21 |
| `idempotency_keys` | `unique (actor_id, key)` | retry replays, never double-sends | 21 |

Both `EXCLUDE` constraints are **partial** (`WHERE deleted_at IS NULL`) so a soft-deleted session releases its slot. Both require `btree_gist` for the `uuid WITH =` operand.

## Legend and reading notes

- **`uuidv7()`** — PG18 native. Time-ordered, so the PK doubles as the keyset cursor and no separate sort column is needed (item 19).
- **`timestamptz` everywhere.** An absolute instant. Local time is rendered by applying `events.timezone` on the way out; it is never stored.
- **`events.timezone`** holds an IANA name (`Asia/Colombo`), never a fixed offset — an offset cannot survive a DST transition, which is the item 12 test.
- **`tstzrange`** rather than `starts_at`/`ends_at` because `EXCLUDE` needs a range type. Two columns would push the overlap check back into Go, which is the check-then-act race the brief is testing for.
- **`version`** is on every mutable row (`events`, `rooms`, `sessions`). Updates are `WHERE id = $1 AND version = $2`; 0 rows affected is a 409, never a last-write-wins.
- **`deleted_at`** — soft delete on `events` and `sessions` only. Membership, rooms and invitations are removed outright. Because item 16's `EXCLUDE` constraints are partial on this column, soft-deleting a session **releases its room and speaker slot immediately** — an intended product rule (D9, [`requirements.md`](./requirements.md) §7.3), not a side effect.
- **`audit_log.entity_id` has no FK** on purpose: the log must outlive whatever it describes.
- **Grants:** the application role holds `INSERT, SELECT` on `audit_log` and nothing else. Append-only is enforced by the grant, not by discipline.

## Deferred

- **Recurring sessions** (item 23) will add a rules-plus-exceptions pair of tables and a schedule history. Nothing above anticipates it — deliberately, since the shape depends on how far that item gets scoped.
- **Shared physical rooms across events** are out of scope. `rooms` hangs off `events`, so two events in the same building are two rows and a clash between them is not detected. Deliberate — it keeps the `EXCLUDE` constraint covering exactly what the model claims. See [`requirements.md`](./requirements.md) §7.1.
