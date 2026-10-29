-- The owner role is now called 'admin'.
alter table users drop constraint users_role_check;
update users set role = 'admin' where role = 'dueno';
alter table users add constraint users_role_check
    check (role in ('admin', 'credito', 'galpon', 'preventista', 'chofer', 'comercio'));
