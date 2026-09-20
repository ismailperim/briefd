---
title: "ADR-0012: Çoklu para birimi ve kur anlık görüntüleri"
tags: [adr, para-birimi, hazine]
---
# ADR-0012: Çoklu para birimi ve kur anlık görüntüleri

- Durum: kabul edildi
- Tarih: 2025-08-02

## Bağlam

Üye işyerleri giderek daha çok para biriminde ödeme kabul ediyor ama hakedişi
tek para biriminde istiyor. Tahsilat anında dönüştürmek bizi gün içi kur
hareketine maruz bırakıyor ve iade tutarlarını üye işyeri için öngörülemez
yapıyordu.

## Karar

Bakiyeler **para birimi başına** tutulur; tahsilat ya da iadede örtük
dönüşüm yapılmaz. Dönüşüm yalnızca hakedişte, üye işyeri tek hakediş para
birimi seçtiyse, hazine sağlayıcısından 00:00 UTC'de alınan **günlük kur anlık
görüntüsüyle** yapılır. Kullanılan kur hakedişle birlikte saklanır.

EUR cinsinden ifade edilen kurallar ve eşikler (iade onayı, asgari hakediş)
aynı günlük anlık görüntüyü kullanır; böylece bir karar gün boyunca sabittir.

## Sonuçlar

- Tutarlar her zaman tam sayı alt birim + ISO 4217 kodu olarak saklanır. Para
  için asla kayan nokta kullanılmaz.
- Kur anlık görüntüsü işi birinci seviye bağımlılıktır; başarısız olursa
  dönüşümlü hakedişler durur ve hazine uyarılır.
