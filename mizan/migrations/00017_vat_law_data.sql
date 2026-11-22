-- +goose Up
-- VAT, part 2: the law's data (rates by category, the exempt-supply list, the first rule version), amended returns and the apportionment of input tax.
-- Source of the texts: the consolidated Persian text of the VAT law of 1400/03/02 on thdorsan.com (rc.majlis.ir and intamedia.ir were unreachable); see mizan/README.md.

-- Amended returns: revision 0 is the original return, 1.. are amendments that book only the DIFFERENCE to the figures already filed.
ALTER TABLE mz_vat_returns
    ADD COLUMN revision     integer NOT NULL DEFAULT 0,
    ADD COLUMN amends_id    uuid REFERENCES mz_vat_returns (id),
    ADD COLUMN delta_output bigint NOT NULL DEFAULT 0,
    ADD COLUMN delta_input  bigint NOT NULL DEFAULT 0;
ALTER TABLE mz_vat_returns DROP CONSTRAINT mz_vat_returns_year_period_months_period_no_key;
ALTER TABLE mz_vat_returns ADD CONSTRAINT mz_vat_returns_period_revision_key UNIQUE (year, period_months, period_no, revision);

-- Rates by category with the date from which they apply. levy_bp is the municipal-levy part of rate_bp (NULL = not known).
CREATE TABLE mz_vat_rates (
    id             uuid PRIMARY KEY,
    category       text NOT NULL,
    effective_from date NOT NULL,
    rate_bp        integer NOT NULL CHECK (rate_bp BETWEEN 0 AND 20000),
    levy_bp        integer CHECK (levy_bp IS NULL OR (levy_bp >= 0 AND levy_bp <= rate_bp)),
    description    text NOT NULL DEFAULT '',
    verified       boolean NOT NULL DEFAULT false, -- true only when the rate was read from the law text itself
    source         text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (category, effective_from)
);

-- Exempt supplies (art. 9) as versioned data; effective_to NULL = still in force.
CREATE TABLE mz_vat_exemptions (
    id             uuid PRIMARY KEY,
    code           text NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('goods', 'services')),
    description    text NOT NULL,
    article        text NOT NULL,
    effective_from date NOT NULL,
    effective_to   date,
    source         text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (code, effective_from)
);

-- Apportionment of one period's input tax between deductible and non-deductible (art. 8 notes 2-5), booked once per period before the return.
CREATE TABLE mz_vat_apportionments (
    id                  uuid PRIMARY KEY,
    year                integer NOT NULL,
    period_months       integer NOT NULL,
    period_no           integer NOT NULL,
    input_total         bigint NOT NULL,
    direct_taxable      bigint NOT NULL,
    direct_exempt       bigint NOT NULL,
    machinery           bigint NOT NULL,
    common              bigint NOT NULL,
    taxable_sales       bigint NOT NULL,
    exempt_sales        bigint NOT NULL,
    recoverable         bigint NOT NULL,
    non_recoverable     bigint NOT NULL,
    journal_id          uuid,
    idem_key            text NOT NULL UNIQUE,
    actor               text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (year, period_months, period_no)
);

-- First rule version: the law of 1400/03/02, art. 4 (payment by the end of the month after the period) and art. 37 (2% per month).
-- The 3-month period is the season of the circulars (e.g. 200/1404/68 "summer 1404"); the new law text does not define the length itself.
INSERT INTO mz_vat_rules (id, effective_from, period_months, filing_days, filing_month_end, late_penalty_bp, source)
VALUES ('00000000-0000-4000-8000-000000001400', '2021-05-23', 3, 0, true, 200,
        'Law on VAT of 1400/03/02 art. 4 (pay by the end of the month after the period) and art. 37 (2% a month from the earlier of the return deadline and the payment due date); thdorsan.com/law-doc/قانون-مالیات-بر-ارزش-افزوده-ماده-4 and -ماده-37. Season length from the circulars 200/1404/68 and 200/1405/55, not from the article.');

-- General rate: art. 7 says 9%; budget laws added one point as the state share (1403 art. 6 clause ر, 1404 art. 1 clause خ: total 10%). 1405 is from secondary
-- sources only (the proposed 12% was not passed). The municipal levy part (300) is NOT verified from the text: the budget wording "state share" only shows that a split exists.
INSERT INTO mz_vat_rates (id, category, effective_from, rate_bp, levy_bp, description, verified, source) VALUES
 ('00000000-0000-4000-8000-000000001701', 'general', '2021-05-23', 900, 300, 'نرخ عمومی ماده ۷', true, 'Law on VAT 1400/03/02 art. 7 (9%); levy part from secondary sources'),
 ('00000000-0000-4000-8000-000000001702', 'general', '2024-03-20', 1000, 300, 'نرخ عمومی ۱۴۰۳: یک واحد درصد افزوده سهم دولت', true, 'Budget law 1403 clause 6 ر (decision 1402/12/21); thdorsan.com/law-doc/قانون-مالیات-بر-ارزش-افزوده-ماده-7'),
 ('00000000-0000-4000-8000-000000001703', 'general', '2025-03-21', 1000, 300, 'نرخ عمومی ۱۴۰۴: کل نرخ ۱۰ واحد درصد', true, 'Budget law 1404 clause 1 خ (1403/11/08), total rate 10 points'),
 ('00000000-0000-4000-8000-000000001704', 'general', '2026-03-21', 1000, 300, 'نرخ عمومی ۱۴۰۵ (متن قانون بودجه ۱۴۰۵ خوانده نشد)', false, 'Secondary sources only (asriran, mehrnews, zharfban 2026): the 12% proposal was not passed, rate stays 10%'),
 -- Art. 26 special rates (the text read lists them as in force from 1400/03/02).
 ('00000000-0000-4000-8000-000000001711', 'petrol_jet_fuel', '2021-05-23', 3000, NULL, 'بنزین و سوخت هواپیما', true, 'Law on VAT art. 26 a-1'),
 ('00000000-0000-4000-8000-000000001712', 'gasoil_kerosene_fuel_oil_gas', '2021-05-23', 1500, NULL, 'نفتگاز، نفت سفید، نفت کوره، گاز طبیعی و گاز مایع', true, 'Law on VAT art. 26 a-2'),
 ('00000000-0000-4000-8000-000000001713', 'gold_jewelry_fee', '2021-05-23', 900, NULL, 'اجرت ساخت، حق‌العمل و سود فروشنده طلا و جواهر', true, 'Law on VAT art. 26 b-2 (9%)'),
 ('00000000-0000-4000-8000-000000001714', 'gold_jewelry_fee', '2025-03-21', 1000, NULL, 'اجرت ساخت طلا ۱۴۰۴: کل نرخ ۱۰', true, 'Budget law 1404 clause 1 خ (applies the +1 point to art. 26 b)'),
 ('00000000-0000-4000-8000-000000001715', 'sugary_drinks_domestic', '2021-05-23', 1600, NULL, 'نوشابه‌های قندی و کالاهای آسیب‌رسان تولید داخل', true, 'Law on VAT art. 26 پ'),
 ('00000000-0000-4000-8000-000000001716', 'sugary_drinks_import', '2021-05-23', 3600, NULL, 'نوشابه‌های قندی و کالاهای آسیب‌رسان وارداتی', true, 'Law on VAT art. 26 پ'),
 ('00000000-0000-4000-8000-000000001717', 'import_exempt_goods_no_domestic_equivalent', '2021-05-23', 300, NULL, 'واردات دارو با کمبود تولید داخل', true, 'Law on VAT art. 9 note 1 (3% at customs)');

-- Tobacco (art. 26 ت) is not seeded: its rates rise by 5 points a year from the second year up to caps, the schedule of years was not read.

INSERT INTO mz_vat_exemptions (id, code, kind, description, article, effective_from, source) VALUES
 (gen_random_uuid(), 'g01', 'goods', 'محصولات کشاورزی فرآوری‌نشده (زراعی، باغی، دارویی، مرتعی، جنگلی، گلخانه‌ای)', 'ماده ۹ الف-۱', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g02', 'goods', 'دام زنده و خوراک آن، مواد اصلی تولید مثل دام، پوسال، کشت بافت', 'ماده ۹ الف-۲', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g03', 'goods', 'بذر، نشاء، نهال، سم و کود', 'ماده ۹ الف-۳', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g04', 'goods', 'آب مصارف کشاورزی', 'ماده ۹ الف-۴', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g05', 'goods', 'شیر، پنیر، ماست، تخم ماکیان، آرد و نان، گوشت (طبق فهرست وزارت بهداشت)، برنج، حبوبات، سویا، روغن خوراکی، شیرخشک کودک، تخم مرغ نطفه‌دار', 'ماده ۹ الف-۵', '2021-05-23', 'Law on VAT art. 9 (food items need a health certificate, note 4)'),
 (gen_random_uuid(), 'g06', 'goods', 'خمیر کاغذ، کاغذ باطله، دفتر تحریر، کاغذ چاپ و تحریر و روزنامه', 'ماده ۹ الف-۶', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g07', 'goods', 'کتاب، روزنامه، مجله و نشریه (کاغذی و الکترونیکی)', 'ماده ۹ الف-۷', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g08', 'goods', 'کالاهای همراه مسافر برای استفاده شخصی تا میزان معافیت قانون امور گمرکی', 'ماده ۹ الف-۸', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g09', 'goods', 'شمش طلا و حواله‌های دارای پشتوانه صددرصد طلا', 'ماده ۹ الف-۹', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g10', 'goods', 'دارایی‌های غیرمنقول و حواله‌های مبتنی بر آنها', 'ماده ۹ الف-۱۰', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g13', 'goods', 'فرش دستباف و مواد اولیه اصلی آن', 'ماده ۹ الف-۱۳', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g14', 'goods', 'صنایع دستی تولید داخل طبق فهرست سالانه', 'ماده ۹ الف-۱۴', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g15', 'goods', 'دارو و واکسن انسانی و دامی، لوازم مصرفی درمانی و توانبخشی', 'ماده ۹ الف-۱۵', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'g16', 'goods', 'رادار و تجهیزات کمک‌ناوبری، تجهیزات نظامی، انتظامی، امنیتی و اطلاعاتی', 'ماده ۹ الف-۱۶ و ۱۷', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's01', 'services', 'خدمات درمانی، تشخیصی، پیشگیری، توانبخشی و آرامستان‌ها', 'ماده ۹ ب-۱', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's02', 'services', 'خدمات درمانی دامی و گیاهی، مکانیزاسیون آب کشاورزی، کشت بافت', 'ماده ۹ ب-۲', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's03', 'services', 'خدمات زینک، چاپ، انتشار و توزیع روزنامه، کتاب و نشریه (به‌جز مؤسسات کمک‌آموزشی و تبلیغات کالاهای داخلی)', 'ماده ۹ ب-۳', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's04', 'services', 'خدمات در برابر حقوق و دستمزد مشمول فصل مالیات بر درآمد حقوق (بخش حقوق قرارداد شرکت‌های تأمین نیرو)', 'ماده ۹ ب-۴', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's05', 'services', 'عملیات و خدمات بانکی (سپرده، تسهیلات، ضمانت‌نامه، انتقال وجه)', 'ماده ۹ ب-۵', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's06', 'services', 'خدمات قرض‌الحسنه صندوق‌های دارای مجوز', 'ماده ۹ ب-۶', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's07', 'services', 'اعطای تسهیلات صندوق‌های حمایتی، ضمانت صادرات، سرمایه‌گذاری، بیمه‌ای و پژوهش و فناوری', 'ماده ۹ ب-۷', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's08', 'services', 'بیمه زندگی، محصولات کشاورزی، اجتماعی و درمان تکمیلی', 'ماده ۹ ب-۸', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's09', 'services', 'خدمات بازار سرمایه، انتشار و نقل و انتقال سهام و اوراق، صدور و ابطال واحدهای صندوق سرمایه‌گذاری', 'ماده ۹ ب-۹ تا ۱۱', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's12', 'services', 'سود تسهیلات شرکت‌های لیزینگ دارای مجوز (نه بهای دارایی)', 'ماده ۹ ب-۱۲', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's13', 'services', 'حمل و نقل بار و مسافر جاده‌ای، ریلی و دریایی درون‌شهری، برون‌شهری و بین‌المللی', 'ماده ۹ ب-۱۳', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's14', 'services', 'خدمات آموزشی، پژوهشی و ورزشی دارای مجوز', 'ماده ۹ ب-۱۴', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's15', 'services', 'اقامت در هتل سه‌ستاره و پایین‌تر، مهمان‌پذیر و مراکز اقامتی دارای مجوز', 'ماده ۹ ب-۱۵', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 's16', 'services', 'شارژ شرکت‌های خدماتی شهرک‌های صنعتی', 'ماده ۹ ب-۱۶', '2021-05-23', 'Law on VAT art. 9'),
 (gen_random_uuid(), 'x01', 'goods', 'صادرات کالا و خدمات (با مسترد شدن مالیات خرید نهاده‌ها)', 'ماده ۱۰ الف', '2021-05-23', 'Law on VAT art. 10 (zero-rated: input tax is refunded)');

-- +goose Down
DROP TABLE mz_vat_apportionments, mz_vat_exemptions, mz_vat_rates;
DELETE FROM mz_vat_rules WHERE id = '00000000-0000-4000-8000-000000001400';
ALTER TABLE mz_vat_returns DROP CONSTRAINT mz_vat_returns_period_revision_key;
ALTER TABLE mz_vat_returns DROP COLUMN revision, DROP COLUMN amends_id, DROP COLUMN delta_output, DROP COLUMN delta_input;
ALTER TABLE mz_vat_returns ADD CONSTRAINT mz_vat_returns_year_period_months_period_no_key UNIQUE (year, period_months, period_no);
