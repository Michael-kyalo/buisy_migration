-- Test-only stand-ins for objects schema/customerappdb.sql references but doesn't
-- define. The trigger functions are no-ops here.
CREATE TABLE public.countries (id serial PRIMARY KEY, name text);
CREATE TABLE public.products (id serial PRIMARY KEY, name text);
CREATE TABLE public.product_eligibility_configs (id bigserial PRIMARY KEY);
CREATE TYPE public.e_account_type AS ENUM ('PAYGO', 'CASH');

CREATE FUNCTION update_contact_general_status_from_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RETURN NEW; END $$;
CREATE FUNCTION audit_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RETURN NEW; END $$;
CREATE FUNCTION update_calls_phone() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RETURN NEW; END $$;
CREATE FUNCTION update_contact_general_status_from_contact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RETURN NEW; END $$;
