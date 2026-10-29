-- Hosted platforms such as Supabase expose every table of the public schema
-- through an HTTP API that anyone holding the (public) anon key can call.
-- This backend is the only intended client: it connects as the table owner,
-- which is not subject to row level security, so turning RLS on without any
-- policy closes that side door and changes nothing for the application.
--
-- Tables created by later migrations must repeat the statement:
--   alter table <name> enable row level security;
do $$
declare
    t text;
begin
    for t in select tablename from pg_tables where schemaname = 'public' loop
        execute format('alter table public.%I enable row level security', t);
    end loop;
end
$$;
