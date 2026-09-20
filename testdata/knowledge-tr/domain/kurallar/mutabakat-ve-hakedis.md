---
title: Mutabakat ve hakediş
tags: [odeme, mutabakat, hakedis, hazine]
---
# Mutabakat ve hakediş

## Mutabakat döngüsü

Alıcı bankalar mutabakat dosyalarını günde bir kez gönderir. İç mutabakat
partimizi **00:00 UTC**'de keser ve her alıcı banka dosyasını o partideki
tahsilatlarla eşleştiririz. Tüm tahsilatlar eşleşirse parti `reconciled`,
eşleşmeyen kalemler hazine kuyruğuna gittiyse `reconciled_with_exceptions`
olur. Partiler yeniden açılmaz; geç gelen kalemler `late_settlement` işaretiyle
sonraki partiye eklenir.

## Üye işyeri bakiyesi

Her üye işyerinin para birimi başına bir bakiyesi vardır:

- **kullanılabilir** — tahsil edilip mutabakatı yapılmış fonlar eksi iadeler,
  ücretler ve rezerv
- **bekleyen** — tahsil edilmiş ama alıcı bankanın henüz mutabakatını
  yapmadığı fonlar
- **rezerv** — ters ibrazları karşılamak için 90 gün tutulan kayan yüzde
  (varsayılan %5, riske göre ayarlanabilir)

Yalnızca kullanılabilir bakiye hakedişe konu olur.

## Hakediş takvimleri

Üye işyeri şunlardan birini seçer:

- `daily` — her iş günü, üye işyerinin saat diliminde 06:00'da
- `weekly` — seçilen bir gün
- `manual` — üye işyeri panelden ya da API'den tetikler

Hakediş yalnızca kullanılabilir bakiye asgari hakediş tutarını (varsayılan 50
EUR karşılığı) aşarsa oluşturulur; altında kalanlar sonraki çalıştırmaya devreder.

## Hakediş hataları

Banka bir hakedişi reddederse (kapalı hesap, hatalı IBAN) fonlar kullanılabilir
bakiyeye döner ve üye işyeri bilgilendirilir. Art arda **üç** başarısızlıktan
sonra takvim `manual`'a çevrilir ve hesap KYC incelemesi için işaretlenir.

## Ücretler

Ücretler tahsilat anında, `merchant_payable`'dan `fee_revenue`'ya ayrı bir defter
kaydıyla düşülür. Ücret tarifeleri üye işyeri bazında ve sürümlüdür; bir
tahsilat her zaman o anda geçerli tarife sürümünü kullanır.
