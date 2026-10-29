-- Application settings as key / JSON value. The BCV rate of the day lives here
-- (key 'bcv_rate') and every platform reads it through the API.
create table settings (
    key        text primary key,
    value      jsonb       not null,
    updated_at timestamptz not null default now()
);

alter table settings enable row level security;

-- Carry over the latest rate already recorded, if any.
insert into settings (key, value, updated_at)
select 'bcv_rate',
       jsonb_build_object('rate', rate, 'effectiveOn', to_char(effective_on, 'YYYY-MM-DD'),
                          'updatedAt', to_char(created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), 'source', 'manual'),
       created_at
from exchange_rates
order by id desc
limit 1;
