-- A commerce (customer business) can have its own login: role 'comercio' linked
-- to exactly one customer, and one login per customer.
alter table users add column customer_id bigint references customers (id);

-- Replace the role / truck checks of 0003 (their generated names are not relied upon).
do $$
declare c record;
begin
    for c in select conname from pg_constraint where conrelid = 'public.users'::regclass and contype = 'c' loop
        execute format('alter table users drop constraint %I', c.conname);
    end loop;
end $$;

alter table users add constraint users_role_check
    check (role in ('dueno', 'credito', 'galpon', 'preventista', 'chofer', 'comercio'));
alter table users add constraint users_truck_check check ((role = 'chofer') = (truck_id is not null));
alter table users add constraint users_customer_check check ((role = 'comercio') = (customer_id is not null));
create unique index users_one_login_per_customer on users (customer_id) where customer_id is not null;
