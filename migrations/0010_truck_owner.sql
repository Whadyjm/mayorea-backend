-- A truck belongs to the distributor (customer_id is null) or to a commerce that runs its own fleet.
alter table trucks add column customer_id bigint references customers (id) on delete cascade;
create index trucks_customer_idx on trucks (customer_id);
