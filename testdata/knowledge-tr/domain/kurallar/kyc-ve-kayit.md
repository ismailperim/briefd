---
title: KYC ve üye işyeri kaydı
tags: [uyum, kyc, kayit]
---
# KYC ve üye işyeri kaydı

## Seviyeler

Üye işyerleri, tam doğrulamadan önce limitleri belirleyen üç seviyeden birine
alınır:

| Seviye | Gereksinim | Aylık hacim tavanı | Hakediş |
|---|---|---|---|
| `starter` | e-posta, telefon, işletme adı | 5.000 EUR | bekletilir |
| `verified` | + sicil belgesi, temsilci kimliği | 100.000 EUR | günlük |
| `enterprise` | + gerçek faydalanıcılar, mali tablolar | yok | her takvim |

`starter` seviyesinde ödeme kabul edilebilir ama hakediş `verified` olana kadar
bekletilir.

## Belge incelemesi

Belgeler uyum ekibi tarafından **iki iş günü** içinde incelenir. Önce otomatik
kontroller çalışır: belge geçerlilik tarihi, sicildeki adla eşleşme, şirketin ve
%25 ve üzeri pay sahibi her gerçek faydalanıcının yaptırım taraması. Tarama
sonucu pozitifse bir insan temizleyene kadar kayıt bloke olur; üye işyerine
yalnızca doğrulamanın beklemede olduğu söylenir.

## Yeniden doğrulama

Doğrulama **24 ay** sonra ya da temsilci/adres değişince veya aylık hacim
son ortalamanın on katını aşınca hemen sona erer. Hâlâ geçerli belgeler yeniden
kullanılır.

## Yasaklı işler

Kumar, yetişkin içeriği, silah ve kripto para borsaları seviyeden bağımsız
olarak yasaktır. Yasaklı listedeki bir MCC ile gelen başvuru `mcc_prohibited`
ile otomatik reddedilir.
