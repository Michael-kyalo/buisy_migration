-- Nexus deliverydb (Postgres): actual table definitions.
-- Referenced but not included: the log_status_to_intransit trigger function (the
-- integration test stubs it).

-- public.companies definition

-- Drop table

-- DROP TABLE public.companies;

CREATE TABLE public.companies (
	id bigserial NOT NULL,
	"name" text NULL,
	created_at timestamptz NULL,
	createdby text NULL,
	updated_at timestamptz NULL,
	updatedby text NULL,
	code text NULL,
	currency_code text NULL,
	currency_symbol text NULL,
	vat_prod_posting_group text NULL,
	dimension_code text NULL,
	cash_sale_customer text NULL,
	created_by text NULL,
	updated_by text NULL,
	country text NULL,
	CONSTRAINT companies_pkey PRIMARY KEY (id),
	CONSTRAINT uni_companies_code UNIQUE (code),
	CONSTRAINT uni_companies_name UNIQUE (name)
);

-- public.locations definition

-- Drop table

-- DROP TABLE public.locations;

CREATE TABLE public.locations (
	id bigserial NOT NULL,
	parent_location_id int8 NULL,
	"name" text NULL,
	code text NULL,
	bin text NULL,
	"type" text NULL,
	country_iso_code text NULL,
	description text NULL,
	company_id int8 NULL,
	auto_recieve bool DEFAULT false NULL,
	status text DEFAULT 'ACTIVE'::text NULL,
	created_at timestamptz NULL,
	updated_at timestamptz NULL,
	created_by text NULL,
	updated_by text NULL,
	CONSTRAINT locations_company_id_name_code_key UNIQUE (company_id, name, code),
	CONSTRAINT locations_pkey PRIMARY KEY (id)
);


-- public.locations foreign keys

ALTER TABLE public.locations ADD CONSTRAINT fk_locations_company FOREIGN KEY (company_id) REFERENCES public.companies(id);
ALTER TABLE public.locations ADD CONSTRAINT fk_locations_parent_location FOREIGN KEY (parent_location_id) REFERENCES public.locations(id);

-- public.dimensions definition

-- Drop table

-- DROP TABLE public.dimensions;

CREATE TABLE public.dimensions (
	id bigserial NOT NULL,
	code text NULL,
	dimension_code text NULL,
	"name" text NULL,
	"type" text NULL,
	created_at timestamptz NULL,
	updated_at timestamptz NULL,
	created_by text NULL,
	updated_by text NULL,
	CONSTRAINT dimensions_dimension_code_code_key UNIQUE (dimension_code, code),
	CONSTRAINT dimensions_pkey PRIMARY KEY (id)
);
-- public.skus definition

-- Drop table

-- DROP TABLE public.skus;

CREATE TABLE public.skus (
	id bigserial NOT NULL,
	price numeric NULL,
	currency text NULL,
	classification text NULL,
	color text NULL,
	base_unit text NULL,
	category_type text NULL,
	serial_number_length int8 NULL,
	stock int8 NULL,
	has_warranty bool DEFAULT false NULL,
	warranty_period int8 NULL,
	code text NULL,
	generation text NULL,
	"name" text NULL,
	status text NULL,
	created_at timestamptz NULL,
	updated_at timestamptz NULL,
	created_by text NULL,
	company_id int8 NULL,
	is_paygo bool DEFAULT false NULL,
	updated_by text NULL,
	tracking_mode text DEFAULT 'SERIAL_NUMBER'::text NULL,
	is_bundled bool DEFAULT false NULL,
	gs1_code text NULL,
	CONSTRAINT skus_company_id_code_key UNIQUE (company_id, code),
	CONSTRAINT skus_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_skus_gs1_code ON public.skus USING btree (gs1_code) WHERE (gs1_code IS NOT NULL);
CREATE INDEX idx_skus_tracking_mode ON public.skus USING btree (tracking_mode);
CREATE UNIQUE INDEX uq_skus_company_gs1_code ON public.skus USING btree (company_id, gs1_code) WHERE (gs1_code IS NOT NULL);


-- public.skus foreign keys

ALTER TABLE public.skus ADD CONSTRAINT fk_skus_company FOREIGN KEY (company_id) REFERENCES public.companies(id);

-- public.agent_skus definition

-- Drop table

-- DROP TABLE public.agent_skus;

CREATE TABLE public.agent_skus (
	id bigserial NOT NULL,
	agent_email text NULL,
	sku_id int8 NULL,
	created_at timestamptz NULL,
	updated_at timestamptz NULL,
	created_by text NULL,
	updated_by text NULL,
	CONSTRAINT agent_skus_pkey PRIMARY KEY (id)
);


-- public.agent_skus foreign keys

ALTER TABLE public.agent_skus ADD CONSTRAINT fk_agent_skus_sku FOREIGN KEY (sku_id) REFERENCES public.skus(id);
-- public.location_users definition

-- Drop table

-- DROP TABLE public.location_users;

CREATE TABLE public.location_users (
	id bigserial NOT NULL,
	location_id int8 NULL,
	external_id text NULL,
	email text NULL,
	created_at timestamptz NULL,
	updated_at timestamptz NULL,
	createdby text NULL,
	updatedby text NULL,
	is_biomass bool DEFAULT false NULL,
	CONSTRAINT location_users_location_id_email_key UNIQUE (location_id, email),
	CONSTRAINT location_users_pkey PRIMARY KEY (id)
);


-- public.location_users foreign keys

ALTER TABLE public.location_users ADD CONSTRAINT fk_location_users_location FOREIGN KEY (location_id) REFERENCES public.locations(id);

-- public.inventory_products definition

-- Drop table

-- DROP TABLE public.inventory_products;

CREATE TABLE public.inventory_products (
	id bigserial NOT NULL,
	sku_id int8 NULL,
	serial_type text NULL,
	scan_in_role text NULL,
	scan_out_role text NULL,
	serial_number text NULL,
	document_no text NOT NULL,
	scan_out_document_no text NULL,
	location_id int8 NULL,
	scan_out_location_id int8 NULL,
	scan_out_by text NULL,
	scan_in_by text NULL,
	scan_out_time timestamptz NULL,
	scan_in_time timestamptz NULL,
	company_id int8 NULL,
	status text NULL,
	metadata jsonb DEFAULT '{}'::jsonb NULL,
	manufacture_date timestamptz NULL,
	created_at timestamptz NULL,
	created_by text NULL,
	updated_at timestamptz NULL,
	updated_by text NULL,
	is_return bool DEFAULT false NOT NULL,
	is_quarantined bool DEFAULT false NOT NULL,
	dimension_country_code text NULL,
	agent text NULL,
	destination_role text NULL,
	sah_status text NULL,
	lost bool DEFAULT false NOT NULL,
	product_condition text NULL,
	CONSTRAINT inventory_products_pkey PRIMARY KEY (id),
	CONSTRAINT uni_inventory_products_serial_number UNIQUE (serial_number)
);
CREATE INDEX idx_inventory_products_agent_status ON public.inventory_products USING btree (agent, status);
CREATE INDEX idx_inventory_products_created_at ON public.inventory_products USING btree (created_at);

-- Table Triggers

create trigger trg_status_to_intransit after
update
    on
    public.inventory_products for each row
    when (((old.status is distinct
from
    new.status)
    and (new.status = 'IN_TRANSIT'::text))) execute function log_status_to_intransit();


-- public.inventory_products foreign keys

ALTER TABLE public.inventory_products ADD CONSTRAINT fk_inventory_products_company FOREIGN KEY (company_id) REFERENCES public.companies(id);
ALTER TABLE public.inventory_products ADD CONSTRAINT fk_inventory_products_location FOREIGN KEY (location_id) REFERENCES public.locations(id);
ALTER TABLE public.inventory_products ADD CONSTRAINT fk_inventory_products_scan_out_location FOREIGN KEY (scan_out_location_id) REFERENCES public.locations(id);
ALTER TABLE public.inventory_products ADD CONSTRAINT fk_inventory_products_sku FOREIGN KEY (sku_id) REFERENCES public.skus(id);