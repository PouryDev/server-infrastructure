-- +goose Up
-- Payroll, part 3: the annual bonus and the overtime cap as rule-version data, the official holiday calendar as data, and a written-off employee loan.

-- Bonus (عیدی و پاداش سالانه) and the overtime cap, added to the Terms of the seeded 1405 version. SECONDARY sources only (no official host could be read): the stand-alone law of 1370/12/06
-- on the annual bonus of workers: 60 days of wage per year, at most 90 days of the legal minimum daily wage, prorated by days of service / 365, not insurable (SSA circular 4195),
-- income tax: exempt up to 1/12 of the annual exemption (Direct Taxes Law art. 91 item 10) = the 0% bracket of the monthly table, the excess is taxable. Labour Law art. 59: overtime at most 4 hours a day.
UPDATE mz_pay_rule_versions
   SET terms = terms || '{"bonus":{"days_per_year":60,"cap_min_wage_days":90,"exempt_amount":0},"max_overtime_minutes_per_day":240}'::jsonb,
       source = source || '; bonus and overtime cap UNVERIFIED (secondary sources only)'
 WHERE id = '00000000-0000-4000-8000-000000001405';

-- The official public holidays, by Jalali date (lunar holidays move every year: a new year needs a new list). Used to check holiday work claimed on a payslip.
CREATE TABLE mz_pay_holidays (
    year     int NOT NULL,
    month    int NOT NULL CHECK (month BETWEEN 1 AND 12),
    day      int NOT NULL CHECK (day BETWEEN 1 AND 31),
    name     text NOT NULL,
    kind     text NOT NULL DEFAULT 'official' CHECK (kind IN ('official', 'workers')),
    verified boolean NOT NULL DEFAULT false,
    source   text NOT NULL,
    PRIMARY KEY (year, month, day)
);
INSERT INTO mz_pay_holidays (year, month, day, name, verified, source)
SELECT 1405, m, d, n, false, 'Council of Public Culture calendar 1405 as reported by rahbordemoaser.ir and parsine.com (secondary; 25 days)' FROM (VALUES
 (1,1,'Eid al-Fitr / Nowruz'),(1,2,'Nowruz / extra Eid al-Fitr holiday'),(1,3,'Nowruz'),(1,4,'Nowruz'),(1,12,'Islamic Republic Day'),(1,13,'Nature Day'),
 (3,6,'Eid al-Adha'),(3,14,'Eid al-Ghadir; Khomeini death anniversary'),(3,15,'15 Khordad uprising'),
 (4,3,'Tasua'),(4,4,'Ashura'),(5,13,'Arbaeen'),(5,21,'Prophet demise; Imam Hasan martyrdom'),(5,22,'Imam Reza martyrdom'),(5,30,'Imam Hasan Askari martyrdom'),
 (6,8,'Prophet birth; Imam Sadiq birth'),(8,22,'Martyrdom of Fatima (as)'),(10,2,'Birth of Imam Ali; Father''s Day'),(10,16,'Mab''ath'),
 (11,4,'Birth of Imam Mahdi, Nim-e Sha''ban'),(11,22,'Revolution Victory'),(12,9,'Imam Ali martyrdom'),(12,19,'Eid al-Fitr'),(12,20,'Extra Eid al-Fitr holiday'),(12,29,'Oil Nationalisation Day')
) AS t(m, d, n);
-- 25 Farvardin 1405 (martyrdom of Imam Sadiq) is a holiday in two sources and not in two others: listed as unverified, add it with POST /payroll/holidays if it is official.
-- Workers' Day (11 Ordibehesht, Labour Law art. 63) is a holiday for workers only.
INSERT INTO mz_pay_holidays (year, month, day, name, kind, verified, source) VALUES (1405, 2, 11, 'Workers'' Day', 'workers', false, 'Labour Law art. 63 (mirror text, secondary)');

ALTER TABLE mz_pay_loans ADD COLUMN written_off bigint NOT NULL DEFAULT 0 CHECK (written_off >= 0), ADD COLUMN write_off_journal uuid, ADD COLUMN write_off_reason text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE mz_pay_loans DROP COLUMN written_off, DROP COLUMN write_off_journal, DROP COLUMN write_off_reason;
DROP TABLE mz_pay_holidays;
UPDATE mz_pay_rule_versions SET terms = terms - 'bonus' - 'max_overtime_minutes_per_day' WHERE id = '00000000-0000-4000-8000-000000001405';
