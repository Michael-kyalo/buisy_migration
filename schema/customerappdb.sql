-- Nexus customerappdb (Postgres): actual table definitions for contacts and accounts.
-- contacts is listed before accounts because accounts has a foreign key to it.
-- Referenced but not included: countries, products, product_eligibility_configs, the
-- e_account_type enum and the trigger functions (the integration test stubs them).

-- public.contacts definition

-- Drop table

-- DROP TABLE public.contacts;

CREATE TABLE public.contacts (
	id serial4 NOT NULL,
	first_name varchar(255) NOT NULL,
	last_name varchar(255) NOT NULL,
	other_names varchar(255) NULL,
	identification_type varchar(20) NULL,
	identification_number varchar(255) NULL,
	phone varchar(20) NULL,
	carbon_score varchar(20) NULL,
	credit_score varchar(20) NULL,
	pos_latitude varchar(20) NULL,
	pos_longitude varchar(20) NULL,
	address varchar(255) NULL,
	salesperson_id varchar(64) NULL,
	region varchar(100) NULL,
	sub_region varchar(100) NULL,
	country_id int4 NULL,
	occupation varchar(255) NULL,
	status varchar(20) NULL,
	created_at timestamp DEFAULT now() NULL,
	updated_at timestamp DEFAULT now() NULL,
	metadata jsonb NULL,
	gender varchar(6) NULL,
	alt_msisdn varchar(20) NULL,
	street_address text NULL,
	created_by varchar(255) NULL,
	updated_by varchar(255) NULL,
	update_reason varchar(255) NULL,
	otp varchar(100) NULL,
	app_enabled bool DEFAULT false NULL,
	"location" varchar(100) NULL,
	"language" varchar(75) DEFAULT 'en'::character varying NULL,
	labels _text NULL,
	customer varchar(1024) NULL,
	contact_group varchar(255) NULL,
	countryname varchar(255) NULL,
	customfields jsonb NULL,
	customer_type jsonb NULL,
	checklist jsonb DEFAULT '{"Customer Details": {"Last Name": "", "First Name": "", "Phone Type": "", "Primary Phone Number": "", "Alternative Phone Number": ""}, "Next of Kin Contacts": {"Last Name": "", "First Name": "", "Phone Number": "", "Relationship": "", "Alternative Phone Number": ""}}'::jsonb NULL,
	home_latitude varchar(55) NULL,
	home_longitude varchar(55) NULL,
	tcr_id varchar(55) NULL,
	home_location jsonb NULL,
	is_referred bool DEFAULT false NOT NULL,
	referred_by int8 NULL,
	phone_hash varchar(64) NULL,
	referral_agent text NULL,
	referral_verifier text NULL,
	referral_status text NULL,
	consent_start timestamp NULL,
	general_status varchar(128) DEFAULT 'ELIGIBLE'::character varying NULL,
	has_marketing_consent bool NULL,
	auto_qualification_status varchar(50) NULL,
	rejection_reasons jsonb NULL,
	rejection_category varchar(100) NULL,
	product_eligibility_config_id int8 NULL,
	CONSTRAINT contacts_phone_key UNIQUE (phone),
	CONSTRAINT contacts_pkey PRIMARY KEY (id)
);
CREATE INDEX contacts_country_id_idx ON public.contacts USING btree (country_id DESC);
CREATE INDEX contacts_country_salesperson_aggregate ON public.contacts USING btree (country_id DESC, salesperson_id DESC);
CREATE INDEX contacts_created_at_idx ON public.contacts USING btree (created_at DESC);
CREATE INDEX contacts_general_status_country_id_idx ON public.contacts USING btree (general_status, country_id);
CREATE INDEX contacts_general_status_idx ON public.contacts USING btree (general_status);
CREATE INDEX contacts_id_country_id_status_salesperson_idx ON public.contacts USING btree (id DESC, country_id DESC, status DESC, salesperson_id DESC);
CREATE INDEX contacts_id_status_aggregate_idx ON public.contacts USING btree (id DESC, status DESC);
CREATE UNIQUE INDEX contacts_phone_hash_key ON public.contacts USING btree (phone_hash) WHERE ((phone_hash)::text <> ''::text);
CREATE INDEX contacts_phone_idx ON public.contacts USING btree (phone DESC);
CREATE INDEX contacts_salesperson_id_idx ON public.contacts USING btree (salesperson_id DESC);
CREATE INDEX contacts_status_idx ON public.contacts USING btree (status DESC, id DESC);
CREATE INDEX idx_contacts_country_id ON public.contacts USING btree (country_id);
CREATE INDEX idx_contacts_country_status ON public.contacts USING btree (country_id, status);
CREATE INDEX idx_contacts_created_at ON public.contacts USING btree (created_at DESC);
CREATE INDEX idx_contacts_general_status ON public.contacts USING btree (general_status);
CREATE INDEX idx_contacts_metadata_cluster ON public.contacts USING btree (((NULLIF((metadata ->> 'cluster_id'::text), ''::text))::integer));
CREATE INDEX idx_contacts_phone ON public.contacts USING btree (phone);
CREATE INDEX idx_contacts_referred_by ON public.contacts USING btree (referred_by);
CREATE INDEX idx_contacts_salesperson_id ON public.contacts USING btree (salesperson_id);
CREATE INDEX idx_contacts_status ON public.contacts USING btree (status);
CREATE INDEX idx_contacts_tcr_id ON public.contacts USING btree (tcr_id) WHERE (tcr_id IS NOT NULL);
CREATE INDEX idx_contacts_validated_country_sorted ON public.contacts USING btree (country_id, updated_at DESC) WHERE ((status)::text <> ALL (ARRAY[('PENDING CONSENT'::character varying)::text, ('PENDING PROSPECT'::character varying)::text]));

-- Table Triggers

create trigger audit_update_trigger after
update
    of first_name,
    last_name,
    phone,
    alt_msisdn,
    region,
    sub_region,
    street_address,
    address on
    public.contacts for each row execute function audit_update();
create trigger trigger_update_calls_phone after
update
    of phone on
    public.contacts for each row execute function update_calls_phone();
create trigger trg_update_contact_general_status_from_contact before
update
    of status on
    public.contacts for each row
    when (((new.status)::text is distinct
from
    (old.status)::text)) execute function update_contact_general_status_from_contact();


-- public.contacts foreign keys

ALTER TABLE public.contacts ADD CONSTRAINT contacts_country_id_fkey FOREIGN KEY (country_id) REFERENCES public.countries(id);
ALTER TABLE public.contacts ADD CONSTRAINT fk_contacts_product_eligibility_config_id FOREIGN KEY (product_eligibility_config_id) REFERENCES public.product_eligibility_configs(id);
ALTER TABLE public.contacts ADD CONSTRAINT fk_contacts_referred_by FOREIGN KEY (referred_by) REFERENCES public.contacts(id) ON DELETE SET NULL;

-- public.accounts definition

-- Drop table

-- DROP TABLE public.accounts;

CREATE TABLE public.accounts (
	id serial4 NOT NULL,
	delivery_date timestamp NULL,
	contact_id int4 NULL,
	product_id int4 NULL,
	external_id varchar(20) NOT NULL,
	serial_number varchar(20) NULL,
	payplan varchar(72) NULL,
	status varchar(32) NULL,
	metadata jsonb NULL,
	created_at timestamp DEFAULT now() NULL,
	updated_at timestamp DEFAULT now() NULL,
	installation_form json NULL,
	account_type public."e_account_type" DEFAULT 'PAYGO'::e_account_type NULL,
	payplan_type varchar(20) NULL,
	created_by varchar(255) NULL,
	updated_by varchar(255) NULL,
	sale_invoice varchar(55) NULL,
	sales_order_no varchar(55) NULL,
	warranty_end_date timestamp DEFAULT now() NULL,
	consent_signed_off bool DEFAULT false NULL,
	verification_state varchar(55) NULL,
	verification_agent varchar(55) NULL,
	verification_start timestamp NULL,
	verification_end timestamp NULL,
	consent_string text NULL,
	consent_path varchar(255) NULL,
	consent_path_merged varchar(255) NULL,
	delivery_schedule jsonb DEFAULT '{"Notes": "", "Preferred Date": "", "Preferred Time": ""}'::jsonb NULL,
	consent_v_id int8 NULL,
	sales_model varchar(20) DEFAULT 'LEGACY'::character varying NULL,
	delivery_method varchar(30) NULL,
	cluster_id int4 NULL,
	cluster_name varchar(255) NULL,
	package_type varchar(16) NULL,
	package_id int8 NULL,
	sales_agent_id varchar(255) NULL,
	CONSTRAINT accounts_external_id_key UNIQUE (external_id),
	CONSTRAINT accounts_package_type_chk CHECK (((package_type IS NULL) OR ((package_type)::text = ANY ((ARRAY['BUNDLED'::character varying, 'UNBUNDLED'::character varying])::text[])))),
	CONSTRAINT accounts_pkey PRIMARY KEY (id)
);
CREATE INDEX accounts_aggregate_index ON public.accounts USING btree (contact_id DESC, product_id DESC, id DESC);
CREATE INDEX accounts_contact_id_idx ON public.accounts USING btree (contact_id DESC, status DESC);
CREATE INDEX accounts_created_at_idx ON public.accounts USING btree (created_at DESC);
CREATE INDEX accounts_delivery_date_idx ON public.accounts USING btree (delivery_date DESC);
CREATE INDEX accounts_id_contactid_status_idx ON public.accounts USING btree (id DESC, contact_id DESC, status DESC);
CREATE INDEX accounts_product_id_idx ON public.accounts USING btree (product_id DESC);
CREATE INDEX accounts_serial_number_idx ON public.accounts USING btree (serial_number DESC);
CREATE UNIQUE INDEX accounts_serial_number_uniq_nonempty ON public.accounts USING btree (serial_number) WHERE ((serial_number)::text <> ''::text);
CREATE UNIQUE INDEX accounts_serial_number_uniq_nonempty_idx ON public.accounts USING btree (serial_number) WHERE ((serial_number)::text <> ''::text);
CREATE INDEX accounts_status_aggregate ON public.accounts USING btree (contact_id DESC, product_id DESC, status DESC, external_id DESC);
CREATE INDEX accounts_status_aggregate_idx ON public.accounts USING btree (status DESC, id DESC);
CREATE INDEX accounts_status_idx ON public.accounts USING btree (status DESC);
CREATE INDEX idx_accounts_cluster_id ON public.accounts USING btree (cluster_id);
CREATE INDEX idx_accounts_contact_id ON public.accounts USING btree (contact_id);
CREATE INDEX idx_accounts_delivery_date_desc ON public.accounts USING btree (delivery_date DESC);
CREATE INDEX idx_accounts_external_id ON public.accounts USING btree (external_id);
CREATE INDEX idx_accounts_package_type ON public.accounts USING btree (package_type);
CREATE INDEX idx_accounts_payplan ON public.accounts USING btree (payplan);
CREATE INDEX idx_accounts_product_id ON public.accounts USING btree (product_id);
CREATE INDEX idx_accounts_sales_model ON public.accounts USING btree (sales_model);
CREATE INDEX idx_accounts_serial ON public.accounts USING btree (serial_number);
CREATE INDEX idx_accounts_serial_number ON public.accounts USING btree (serial_number);

-- Table Triggers

create trigger trg_accounts_status_to_contact after
update
    of status on
    public.accounts for each row execute function update_contact_general_status_from_account();
create trigger trg_update_contact_general_status_insert after
insert
    on
    public.accounts for each row execute function update_contact_general_status_from_account();


-- public.accounts foreign keys

ALTER TABLE public.accounts ADD CONSTRAINT accounts_contact_id_fkey FOREIGN KEY (contact_id) REFERENCES public.contacts(id);
ALTER TABLE public.accounts ADD CONSTRAINT accounts_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id);

