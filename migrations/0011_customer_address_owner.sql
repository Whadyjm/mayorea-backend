-- Where a commerce is (for the driver's map) and, for a commerce's own clients, who they belong to
-- (owner_id; null means the distributor's customer).
alter table customers add column address text not null default '';
alter table customers add column lat double precision;
alter table customers add column lng double precision;
alter table customers add constraint customers_geo_pair check ((lat is null) = (lng is null));
alter table customers add column owner_id bigint references customers (id) on delete cascade;
create index customers_owner_idx on customers (owner_id);
