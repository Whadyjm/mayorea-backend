-- Money is stored as integer cents (bigint). Rates are ten-thousandths of Bs/USD.

create table products (
    id         bigint generated always as identity primary key,
    name       text    not null,
    unit       text    not null check (unit in ('bulto', 'caja', 'unidad')),
    price_usd  bigint  not null check (price_usd >= 0),
    committed  integer not null default 0 check (committed >= 0),
    min_stock  integer not null default 0 check (min_stock >= 0)
);

create table lots (
    id         bigint generated always as identity primary key,
    product_id bigint  not null references products (id),
    code       text    not null,
    expires_on date    not null,
    quantity   integer not null check (quantity >= 0),
    location   text    not null default ''
);
create index lots_product_fefo_idx on lots (product_id, expires_on, id) where quantity > 0;

create table customers (
    id               bigint generated always as identity primary key,
    name             text   not null,
    rif              text   not null unique,
    route            text   not null default '',
    phone            text   not null default '',
    credit_limit_usd bigint not null default 0 check (credit_limit_usd >= 0),
    credit_days      integer not null default 0 check (credit_days >= 0)
);

create table trucks (
    id     bigint generated always as identity primary key,
    name   text   not null,
    driver text   not null default '',
    routes text[] not null default '{}'
);

create table exchange_rates (
    id           bigint generated always as identity primary key,
    rate         bigint not null check (rate > 0),
    effective_on date   not null,
    created_at   timestamptz not null default now()
);

create sequence order_number_seq;
create table orders (
    id          bigint generated always as identity primary key,
    number      bigint  not null unique default nextval('order_number_seq'),
    customer_id bigint  not null references customers (id),
    order_date  date    not null,
    salesperson text    not null default '',
    channel     text    not null check (channel in ('ruta', 'whatsapp', 'oficina')),
    terms       text    not null check (terms in ('credito', 'contado')),
    status      text    not null check (status in ('por_aprobar', 'aprobado', 'despachado', 'rechazado')),
    note        text    not null default ''
);
create index orders_customer_idx on orders (customer_id);
create index orders_status_idx on orders (status);

create table order_lines (
    id         bigint generated always as identity primary key,
    order_id   bigint  not null references orders (id) on delete cascade,
    product_id bigint  not null references products (id),
    quantity   integer not null check (quantity > 0),
    price_usd  bigint  not null check (price_usd >= 0)
);
create index order_lines_order_idx on order_lines (order_id);

-- Fiscal correlative (SENIAT): gapless numbering is the operator's responsibility
-- to enforce at print time; the sequence guarantees uniqueness and order.
create sequence invoice_number_seq;
create table invoices (
    id          bigint generated always as identity primary key,
    number      bigint not null unique default nextval('invoice_number_seq'),
    order_id    bigint not null unique references orders (id),
    customer_id bigint not null references customers (id),
    issued_on   date   not null,
    due_on      date   not null,
    total_usd   bigint not null check (total_usd >= 0),
    issue_rate  bigint not null check (issue_rate > 0)
);
create index invoices_customer_idx on invoices (customer_id);

create table payments (
    id               bigint generated always as identity primary key,
    invoice_id       bigint not null references invoices (id),
    paid_on          date   not null,
    cash_usd         bigint not null check (cash_usd >= 0),
    bs               bigint not null check (bs >= 0),
    rate             bigint not null check (rate > 0),
    reference        text   not null default '',
    applied_usd      bigint not null check (applied_usd > 0),
    exchange_diff_bs bigint not null
);
create index payments_invoice_idx on payments (invoice_id);
