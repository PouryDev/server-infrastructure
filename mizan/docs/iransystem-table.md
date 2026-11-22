# Iran System (ایران سیستم) 8-bit code page -> Unicode

## Sources
1. PRIMARY (authoritative, original): Roozbeh Pournader's "IRAN SYSTEM to Unicode" table v1.0 (21 Jan 2000, Unicode 3.0; originally http://www.sharif.ac.ir/~roozbeh/unicode, hosted by Mark Leisher).
   Fetched from: https://web.archive.org/web/2020id_/http://sofia.nmsu.edu/~mleisher/Software/csets/IRANSYSTEM.TXT (13439 bytes). Column 2 below is copied from it verbatim (0x200C = ZWNJ, 0x200D = ZWJ already substituted by Leisher for the original <ZWJ>/<ZWNJ> tags).
2. Wikipedia en "Iran System encoding" (wikitext via https://en.wikipedia.org/w/index.php?title=Iran_System_encoding&action=raw). It cites source 1 as its reference, so it is NOT provenance-independent; it gives Arabic Presentation Forms code points (column 3).
3. python-iransystem 0.5.1 on PyPI (https://pypi.org/project/iransystem/ ; source https://git.sr.ht/~gurban/python-iransystem, wheel from files.pythonhosted.org, only read, never executed). Independent implementation with its own decode table + `_CONTEXTUAL_FORMS` (isolated, final, initial, medial -> byte). Its README even shows reading Tamin Ejtema-ee (Social Security) `DSKWOR00.dbf` with encoding="iransystem" (fields DSW_FNAME, DSW_LNAME).
   NOT found: GNU libiconv lib/iransystem.h (404; libiconv has no IRANSYSTEM), glibc charmaps/IRANSYSTEM (404; glibc has none). fa.wikipedia fetch blocked (Wikimedia error page).

## Agreement between sources (checked programmatically)
- Pournader vs Wikipedia: all 256 cells compared; for 0x80-0xFF base letters agree on every byte after NFKC of Wikipedia's presentation forms (only trivial: 0xFF is NBSP U+00A0 in both; NFKC turns it into a space). Wikipedia's form names (isolated/initial/medial/final, "†" = initial-or-medial, "‡" = final-or-isolated) match Pournader's form labels.
- Pournader vs python-iransystem: decode table identical in base letters for all 128 upper bytes (0 diffs; LAM-ALEF 0xF2 = lam+alef in both). Form assignment (python `_CONTEXTUAL_FORMS`) matches Pournader on all 35 letters: e.g. ALEF 90 iso/91 final; AIN E1 iso/E2 final/E4 initial/E3 medial; HEH F9 iso+final/FB initial/FA medial; FARSI YEH FD iso/FC final/FE initial+medial. 0 mismatches.
- Differences: (a) Pournader table has no row for 0x7F (python maps DELETE; Wikipedia shows CP437 HOUSE U+2302). (b) Wikipedia says lower half 0x00-0x7F is "same as CP437" (i.e. glyphs like smiley at 0x01); Pournader and python treat 0x00-0x7F as plain ASCII control+ASCII. For text data: use ASCII. (c) Pournader merges byte-level forms into ZWJ/ZWNJ markers; python/Wikipedia drop them (base letter only).

## Lower half 0x00-0x7F
Plain ASCII (0x00-0x7E identity per Pournader; 0x7F = DEL in python-iransystem, not listed by Pournader).

## Upper half 0x80-0xFF
Columns: byte | Pournader Unicode sequence (ZWJ=200D, ZWNJ=200C) | Wikipedia presentation-form codepoint | Pournader name/form

| byte | Pournader (Unicode) | Wikipedia glyph cp | name / form |
|---|---|---|---|
| 0x80 | 06F0 | U+06F0 | EXTENDED ARABIC-INDIC DIGIT ZERO |
| 0x81 | 06F1 | U+06F1 | EXTENDED ARABIC-INDIC DIGIT ONE |
| 0x82 | 06F2 | U+06F2 | EXTENDED ARABIC-INDIC DIGIT TWO |
| 0x83 | 06F3 | U+06F3 | EXTENDED ARABIC-INDIC DIGIT THREE |
| 0x84 | 06F4 | U+06F4 | EXTENDED ARABIC-INDIC DIGIT FOUR |
| 0x85 | 06F5 | U+06F5 | EXTENDED ARABIC-INDIC DIGIT FIVE |
| 0x86 | 06F6 | U+06F6 | EXTENDED ARABIC-INDIC DIGIT SIX |
| 0x87 | 06F7 | U+06F7 | EXTENDED ARABIC-INDIC DIGIT SEVEN |
| 0x88 | 06F8 | U+06F8 | EXTENDED ARABIC-INDIC DIGIT EIGHT |
| 0x89 | 06F9 | U+06F9 | EXTENDED ARABIC-INDIC DIGIT NINE |
| 0x8A | 060C | U+060C | ARABIC COMMA |
| 0x8B | 0640 | U+0640 | ARABIC TATWEEL |
| 0x8C | 061F | U+061F | ARABIC QUESTION MARK |
| 0x8D | 200C 0622 | U+FE81 | ARABIC LETTER ALEF WITH MADDA ABOVE, isolated form |
| 0x8E | 0626 200D | U+FE8B | ARABIC LETTER YEH WITH HAMZA ABOVE, initial-medial form |
| 0x8F | 0621 | U+FE80 | ARABIC LETTER HAMZA |
| 0x90 | 200C 0627 | U+FE8D | ARABIC LETTER ALEF, isolated form |
| 0x91 | 200D 0627 | U+FE8E | ARABIC LETTER ALEF, final form |
| 0x92 | 0628 200C | U+FE8F | ARABIC LETTER BEH, final-isolated form |
| 0x93 | 0628 200D | U+FE91 | ARABIC LETTER BEH, initial-medial form |
| 0x94 | 067E 200C | U+FB56 | ARABIC LETTER PEH, final-isolated form |
| 0x95 | 067E 200D | U+FB58 | ARABIC LETTER PEH, initial-medial form |
| 0x96 | 062A 200C | U+FE95 | ARABIC LETTER TEH, final-isolated form |
| 0x97 | 062A 200D | U+FE97 | ARABIC LETTER TEH, initial-medial form |
| 0x98 | 062B 200C | U+FE99 | ARABIC LETTER THEH, final-isolated form |
| 0x99 | 062B 200D | U+FE9B | ARABIC LETTER THEH, initial-medial form |
| 0x9A | 062C 200C | U+FE9D | ARABIC LETTER JEEM, final-isolated form |
| 0x9B | 062C 200D | U+FE9F | ARABIC LETTER JEEM, initial-medial form |
| 0x9C | 0686 200C | U+FB7A | ARABIC LETTER TCHEH, final-isolated form |
| 0x9D | 0686 200D | U+FB7C | ARABIC LETTER TCHEH, initial-medial form |
| 0x9E | 062D 200C | U+FEA1 | ARABIC LETTER HAH, final-isolated form |
| 0x9F | 062D 200D | U+FEA3 | ARABIC LETTER HAH, initial-medial form |
| 0xA0 | 062E 200C | U+FEA5 | ARABIC LETTER KHAH, final-isolated form |
| 0xA1 | 062E 200D | U+FEA7 | ARABIC LETTER KHAH, initial-medial form |
| 0xA2 | 062F | U+FEA9 | ARABIC LETTER DAL |
| 0xA3 | 0630 | U+FEAB | ARABIC LETTER THAL |
| 0xA4 | 0631 | U+FEAD | ARABIC LETTER REH |
| 0xA5 | 0632 | U+FEAF | ARABIC LETTER ZAIN |
| 0xA6 | 0698 | U+FB8A | ARABIC LETTER JEH |
| 0xA7 | 0633 200C | U+FEB1 | ARABIC LETTER SEEN, final-isolated form |
| 0xA8 | 0633 200D | U+FEB3 | ARABIC LETTER SEEN, initial-medial form |
| 0xA9 | 0634 200C | U+FEB5 | ARABIC LETTER SHEEN, final-isolated form |
| 0xAA | 0634 200D | U+FEB7 | ARABIC LETTER SHEEN, initial-medial form |
| 0xAB | 0635 200C | U+FEB9 | ARABIC LETTER SAD, final-isolated form |
| 0xAC | 0635 200D | U+FEBB | ARABIC LETTER SAD, initial-medial form |
| 0xAD | 0636 200C | U+FEBD | ARABIC LETTER DAD, final-isolated form |
| 0xAE | 0636 200D | U+FEBF | ARABIC LETTER DAD, initial-medial form |
| 0xAF | 0637 | U+FEC1 | ARABIC LETTER TAH |
| 0xB0 | 2591 | U+2591 | LIGHT SHADE |
| 0xB1 | 2592 | U+2592 | MEDIUM SHADE |
| 0xB2 | 2593 | U+2593 | DARK SHADE |
| 0xB3 | 2502 | U+2502 | BOX DRAWINGS LIGHT VERTICAL |
| 0xB4 | 2524 | U+2524 | BOX DRAWINGS LIGHT VERTICAL AND LEFT |
| 0xB5 | 2561 | U+2561 | BOX DRAWINGS VERTICAL SINGLE AND LEFT DOUBLE |
| 0xB6 | 2562 | U+2562 | BOX DRAWINGS VERTICAL DOUBLE AND LEFT SINGLE |
| 0xB7 | 2556 | U+2556 | BOX DRAWINGS DOWN DOUBLE AND LEFT SINGLE |
| 0xB8 | 2555 | U+2555 | BOX DRAWINGS DOWN SINGLE AND LEFT DOUBLE |
| 0xB9 | 2563 | U+2563 | BOX DRAWINGS DOUBLE VERTICAL AND LEFT |
| 0xBA | 2551 | U+2551 | BOX DRAWINGS DOUBLE VERTICAL |
| 0xBB | 2557 | U+2557 | BOX DRAWINGS DOUBLE DOWN AND LEFT |
| 0xBC | 255D | U+255D | BOX DRAWINGS DOUBLE UP AND LEFT |
| 0xBD | 255C | U+255C | BOX DRAWINGS UP DOUBLE AND LEFT SINGLE |
| 0xBE | 255B | U+255B | BOX DRAWINGS UP SINGLE AND LEFT DOUBLE |
| 0xBF | 2510 | U+2510 | BOX DRAWINGS LIGHT DOWN AND LEFT |
| 0xC0 | 2514 | U+2514 | BOX DRAWINGS LIGHT UP AND RIGHT |
| 0xC1 | 2534 | U+2534 | BOX DRAWINGS LIGHT UP AND HORIZONTAL |
| 0xC2 | 252C | U+252C | BOX DRAWINGS LIGHT DOWN AND HORIZONTAL |
| 0xC3 | 251C | U+251C | BOX DRAWINGS LIGHT VERTICAL AND RIGHT |
| 0xC4 | 2500 | U+2500 | BOX DRAWINGS LIGHT HORIZONTAL |
| 0xC5 | 253C | U+253C | BOX DRAWINGS LIGHT VERTICAL AND HORIZONTAL |
| 0xC6 | 255E | U+255E | BOX DRAWINGS VERTICAL SINGLE AND RIGHT DOUBLE |
| 0xC7 | 255F | U+255F | BOX DRAWINGS VERTICAL DOUBLE AND RIGHT SINGLE |
| 0xC8 | 255A | U+255A | BOX DRAWINGS DOUBLE UP AND RIGHT |
| 0xC9 | 2554 | U+2554 | BOX DRAWINGS DOUBLE DOWN AND RIGHT |
| 0xCA | 2569 | U+2569 | BOX DRAWINGS DOUBLE UP AND HORIZONTAL |
| 0xCB | 2566 | U+2566 | BOX DRAWINGS DOUBLE DOWN AND HORIZONTAL |
| 0xCC | 2560 | U+2560 | BOX DRAWINGS DOUBLE VERTICAL AND RIGHT |
| 0xCD | 2550 | U+2550 | BOX DRAWINGS DOUBLE HORIZONTAL |
| 0xCE | 256C | U+256C | BOX DRAWINGS DOUBLE VERTICAL AND HORIZONTAL |
| 0xCF | 2567 | U+2567 | BOX DRAWINGS UP SINGLE AND HORIZONTAL DOUBLE |
| 0xD0 | 2568 | U+2568 | BOX DRAWINGS UP DOUBLE AND HORIZONTAL SINGLE |
| 0xD1 | 2564 | U+2564 | BOX DRAWINGS DOWN SINGLE AND HORIZONTAL DOUBLE |
| 0xD2 | 2565 | U+2565 | BOX DRAWINGS DOWN DOUBLE AND HORIZONTAL SINGLE |
| 0xD3 | 2559 | U+2559 | BOX DRAWINGS UP DOUBLE AND RIGHT SINGLE |
| 0xD4 | 2558 | U+2558 | BOX DRAWINGS UP SINGLE AND RIGHT DOUBLE |
| 0xD5 | 2552 | U+2552 | BOX DRAWINGS DOWN SINGLE AND RIGHT DOUBLE |
| 0xD6 | 2553 | U+2553 | BOX DRAWINGS DOWN DOUBLE AND RIGHT SINGLE |
| 0xD7 | 256B | U+256B | BOX DRAWINGS VERTICAL DOUBLE AND HORIZONTAL SINGLE |
| 0xD8 | 256A | U+256A | BOX DRAWINGS VERTICAL SINGLE AND HORIZONTAL DOUBLE |
| 0xD9 | 2518 | U+2518 | BOX DRAWINGS LIGHT UP AND LEFT |
| 0xDA | 250C | U+250C | BOX DRAWINGS LIGHT DOWN AND RIGHT |
| 0xDB | 2588 | U+2588 | FULL BLOCK |
| 0xDC | 2584 | U+2584 | LOWER HALF BLOCK |
| 0xDD | 258C | U+258C | LEFT HALF BLOCK |
| 0xDE | 2590 | U+2590 | RIGHT HALF BLOCK |
| 0xDF | 2580 | U+2580 | UPPER HALF BLOCK |
| 0xE0 | 0638 | U+FEC5 | ARABIC LETTER ZAH |
| 0xE1 | 200C 0639 200C | U+FEC9 | ARABIC LETTER AIN, isolated form |
| 0xE2 | 200D 0639 200C | U+FECA | ARABIC LETTER AIN, final form |
| 0xE3 | 200D 0639 200D | U+FECC | ARABIC LETTER AIN, medial form |
| 0xE4 | 200C 0639 200D | U+FECB | ARABIC LETTER AIN, initial form |
| 0xE5 | 200C 063A 200C | U+FECD | ARABIC LETTER GHAIN, isolated form |
| 0xE6 | 200D 063A 200C | U+FECE | ARABIC LETTER GHAIN, final form |
| 0xE7 | 200D 063A 200D | U+FED0 | ARABIC LETTER GHAIN, medial form |
| 0xE8 | 200C 063A 200D | U+FECF | ARABIC LETTER GHAIN, initial form |
| 0xE9 | 0641 200C | U+FED1 | ARABIC LETTER FEH, final-isolated form |
| 0xEA | 0641 200D | U+FED3 | ARABIC LETTER FEH, initial-medial form |
| 0xEB | 0642 200C | U+FED5 | ARABIC LETTER QAF, final-isolated form |
| 0xEC | 0642 200D | U+FED7 | ARABIC LETTER QAF, initial-medial form |
| 0xED | 06A9 200C | U+FB8E | ARABIC LETTER KEHEH, final-isolated form |
| 0xEE | 06A9 200D | U+FB90 | ARABIC LETTER KEHEH, initial-medial form |
| 0xEF | 06AF 200C | U+FB92 | ARABIC LETTER GAF, final-isolated form |
| 0xF0 | 06AF 200D | U+FB94 | ARABIC LETTER GAF, initial-medial form |
| 0xF1 | 0644 200C | U+FEDD | ARABIC LETTER LAM, final-isolated form |
| 0xF2 | 0644 0627 | U+FEFB | ARABIC LIGATURE LAM WITH ALEF |
| 0xF3 | 0644 200D | U+FEDF | ARABIC LETTER LAM, initial-medial form |
| 0xF4 | 0645 200C | U+FEE1 | ARABIC LETTER MEEM, final-isolated form |
| 0xF5 | 0645 200D | U+FEE3 | ARABIC LETTER MEEM, initial-medial form |
| 0xF6 | 0646 200C | U+FEE5 | ARABIC LETTER NOON, final-isolated form |
| 0xF7 | 0646 200D | U+FEE7 | ARABIC LETTER NOON, initial-medial form |
| 0xF8 | 0648 | U+FEED | ARABIC LETTER WAW |
| 0xF9 | 0647 200C | U+FEE9 | ARABIC LETTER HEH, final-isolated form |
| 0xFA | 200D 0647 200D | U+FEEC | ARABIC LETTER HEH, medial form |
| 0xFB | 200C 0647 200D | U+FEEB | ARABIC LETTER HEH, initial form |
| 0xFC | 200D 06CC 200C | U+FBFD | ARABIC LETTER FARSI YEH, final form |
| 0xFD | 200C 06CC 200C | U+FBFC | ARABIC LETTER FARSI YEH, isolated form |
| 0xFE | 06CC 200D | U+FBFE | ARABIC LETTER FARSI YEH, initial-medial form |
| 0xFF | 00A0 | U+00A0 | NO-BREAK SPACE |

Form summary: letters with 4 distinct shapes use separate bytes only for ALEF (iso 90/final 91), AIN (E1 iso,E2 final,E4 init,E3 medial), GHAIN (E5,E6,E8,E7), HEH (F9 iso+final, FB init, FA medial) and FARSI YEH (FD iso, FC final, FE init+medial). All other dual-joining letters have 2 bytes: one for "final or isolated" (no join to following letter) and one for "initial or medial" (joins following letter). Right-joining-only letters (ALEF MADDA 8D, HAMZA 8F, DAL A2, THAL A3, REH A4, ZAIN A5, JEH A6, WAW F8) and TAH AF / ZAH E0 have one byte. 0xF2 = LAM-ALEF ligature (decodes to U+0644 U+0627 logical; in visual byte order it is a single glyph). 0xB0-0xDF = CP437-style box/shade drawing glyphs (B0-DF), 0xFF = NBSP. Digits 0x80-0x89 are Persian (extended Arabic-Indic) U+06F0-06F9; Latin digits 0x30-0x39 also exist. Persian comma 0x8A=U+060C, tatweel 0x8B=U+0640, Arabic question mark 0x8C=U+061F.

## Visual vs logical order (storage rule)
- Pournader table note 2: "The characters in the IRAN SYSTEM standard are saved, and transmitted in a visual order" (conversion to Unicode with bidi behaviour "may be hard"). Same file, note 3: example "khaane-haa" is coded A1 91 F7 F9 FA 91 = 062E 0627 0646 0647 200C 0647 0627 (NOTE: the example bytes as printed are in the order of the logical letter sequence; real files store them reversed, see below; the example only illustrates form->letter mapping).
- python-iransystem README/source (module comment): "IranSystem text is stored in visual order: each line is laid out left to right as it appears on screen, so Persian text is stored backwards. To get logical order, reverse the line, but keep left-to-right runs (Latin words, numbers) in their original order and swap mirrored characters such as brackets." Its flip(): reverse whole line; then re-reverse each LTR chain (regex over [A-Za-z0-9 and Persian/Arabic digits] with joiners ' . , : ? = & - and affixes + % # $ / @ _ ~ * \ ), and mirror ()[]{}<>«». Chains separated only by spaces form one run if any contains a Latin letter, otherwise each chain (e.g. a number) is its own run. Lines split on CR/LF; each line independently. The author states these rules are his own heuristics made to match real files, not the Unicode Bidi Algorithm.
- Hence: digits (Persian 0x80-0x89 or ASCII) inside Persian text keep LTR order in storage (a number like ۱۲۳ is stored as bytes 81 82 83 left to right in the file, while the surrounding Persian letters are reversed). Decoding algorithm: map bytes -> letters (apply table, drop ZWJ/ZWNJ markers), reverse each line, then re-reverse digit/Latin runs, mirror brackets. Insert ZWNJ where forms show a break (e.g. after 0xF9 final HEH followed by an initial/medial form) if you want proper Unicode.

## Verification with known words (encoded with python-iransystem's form table, decoded with Pournader's table; byte lists are file order = visual, i.e. logical reversed)
- مدیرعامل : F1 F5 91 E4 A4 FE A2 F5 -> decoded (reverse + drop ZW) = مدیرعامل OK
- کارگر ساده : F9 A2 91 A8 20 A4 F0 A4 91 EE -> decoded (reverse + drop ZW) = کارگر ساده OK
- مرد : A2 A4 F5 -> decoded (reverse + drop ZW) = مرد OK
- زن : F6 A5 -> decoded (reverse + drop ZW) = زن OK
- ایرانی : FC F7 90 A4 FE 90 -> decoded (reverse + drop ZW) = ایرانی OK
- خانه : F9 F7 91 A1 -> decoded (reverse + drop ZW) = خانه OK
- سلام : F4 F2 A8 -> decoded (reverse + drop ZW) = سلام OK

Caveat: these round trips prove table/rule self-consistency only; no real DBF bytes were available to me. Verify with a few real records from the Tamin Ejtema-ee DBF (e.g. expect job title bytes ending/starting as above; text starts with the LAST logical letter).
