create table users (
    id            bigint generated always as identity primary key,
    name          text    not null,
    email         text    not null unique,   -- stored lowercase
    password_hash text    not null,
    role          text    not null check (role in ('dueno', 'credito', 'galpon', 'preventista', 'chofer')),
    active        boolean not null default true,
    truck_id      bigint references trucks (id),
    created_at    timestamptz not null default now(),
    -- only drivers run a truck, and every driver has one
    check ((role = 'chofer') = (truck_id is not null))
);

-- token_hash is the SHA-256 of the bearer token; the token itself is never stored.
create table sessions (
    token_hash text primary key,
    user_id    bigint      not null references users (id) on delete cascade,
    expires_at timestamptz not null,
    created_at timestamptz not null default now()
);
create index sessions_user_idx on sessions (user_id);
create index sessions_expires_idx on sessions (expires_at);
