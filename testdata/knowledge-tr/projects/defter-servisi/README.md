---
title: defter-servisi
tags: [defter, servis]
---
# defter-servisi

Kestrel Pay'in çift taraflı defteri ve bakiye projeksiyonları (bkz. ADR-0007).
Go ile yazılmıştır, Postgres kullanır, her bölgede üç replika ile çalışır.

## Sorumluluklar

- Ödeme, iade, hakediş ve ücretlerden gelen kayıt olaylarını kabul etmek
- Üye işyeri ve para birimi bazında bakiye projeksiyonlarını tutmak
- Panel ve hakediş zamanlayıcısına bakiye sorgusu sunmak
- Günlük mutabakat partisini ve raporunu üretmek

## Yeniden deneme

Çağıranlar kayıt gönderimlerini standart politikayla yeniden denemelidir;
defter, kayıt kimliğine göre tekilleştirdiği için tekrarlar güvenlidir.

## Çalışma kitabı: projeksiyon sapması

**Uyarı:** `LedgerProjectionDrift` — gece yeniden inşası canlı projeksiyondan
farklı bakiyeler buldu.

1. Etkilenen üye işyerleri için `ledger_projection_drift_total` metriğine bakın.
2. Projeksiyon tablolarına doğrudan yazılmış kayıt var mı kontrol edin (olmamalı).
3. `ledger rebuild --merchant <id>` ile projeksiyonu olaylardan yeniden inşa edin.
4. Sapma tekrarlarsa defter ekibi liderini çağırın; uyarıyı susturmayın.
