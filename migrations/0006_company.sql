-- The distributor's own profile: one row, always id 1.
create table company (
    id         smallint primary key check (id = 1),
    trade_name text not null,
    legal_name text not null,
    rif        text not null,
    phone      text not null default '',
    email      text not null default '',
    address    text not null default '',
    city       text not null default '',
    state      text not null default '',
    logo       text not null default '',   -- reduced image as a data URL
    updated_at timestamptz not null default now()
);

alter table company enable row level security;
