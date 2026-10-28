-- A truck on the street (viaje) and everything tied to it.

create table trips (
    id                  bigint generated always as identity primary key,
    truck_id            bigint not null references trucks (id),
    driver              text   not null default '',
    trip_date           date   not null,
    status              text   not null check (status in ('en_ruta', 'por_liquidar', 'liquidado')),
    settled_on          date,                       -- set when the driver closes the route
    settlement_cash_usd bigint check (settlement_cash_usd >= 0),
    settlement_note     text   not null default ''
);
-- A truck has at most one trip that is not settled yet.
create unique index trips_one_open_per_truck on trips (truck_id) where status <> 'liquidado';

create table trip_stops (
    trip_id     bigint  not null references trips (id) on delete cascade,
    position    integer not null,
    customer_id bigint  not null references customers (id),
    primary key (trip_id, position)
);

create table trip_orders (
    trip_id   bigint  not null references trips (id) on delete cascade,
    order_id  bigint  not null references orders (id),
    kind      text    not null check (kind in ('dispatch', 'sale', 'presale')),
    delivered boolean not null default false,
    position  integer not null,
    primary key (trip_id, order_id)
);

create table trip_load (
    trip_id    bigint  not null references trips (id) on delete cascade,
    position   integer not null,
    product_id bigint  not null references products (id),
    quantity   integer not null check (quantity > 0),
    primary key (trip_id, position)
);

-- Lots the extra load was taken from, so returned goods go back where they left.
create table trip_origin (
    trip_id  bigint  not null references trips (id) on delete cascade,
    lot_id   bigint  not null references lots (id),
    quantity integer not null check (quantity > 0),
    primary key (trip_id, lot_id)
);

create table trip_returned (
    trip_id    bigint  not null references trips (id) on delete cascade,
    product_id bigint  not null references products (id),
    quantity   integer not null check (quantity > 0),
    primary key (trip_id, product_id)
);

alter table payments add column trip_id bigint references trips (id);
create index payments_trip_idx on payments (trip_id) where trip_id is not null;
