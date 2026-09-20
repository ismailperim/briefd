---
title: Hata yönetimi ve yeniden deneme politikası
tags: [dayaniklilik, yeniden-deneme, konvansiyon]
---
# Hata yönetimi ve yeniden deneme politikası

## Hataların sınıflandırılması

Yeniden denemeden önce hatayı sınıflandırın:

- **Geçici** — zaman aşımı, bağlantı kopması, HTTP 429/502/503/504, veritabanı
  serileştirme hataları. Yeniden denenebilir.
- **Kalıcı** — doğrulama hataları, 429 dışındaki 4xx'ler, iş kuralı redleri.
  Asla yeniden denenmez; çağırana iletilir.
- **Belirsiz** — istek uygulanmış da olabilir olmamış da (gönderdikten sonra
  zaman aşımı). Yalnızca idempotent bir yoldan yeniden denenir.

## Yeniden deneme politikası

Elle yazılmış döngüler yerine `platform/retry` kullanın. Varsayılan politika:

- 200 ms'den başlayan üstel geri çekilme, çarpan 2, **tam jitter**
- en fazla 5 deneme
- toplam bütçe 30 saniye
- `Retry-After` varsa ona uyulur

Alıcı bankaya para taşıyan çağrılar daha katı bir politika kullanır: 3 deneme,
10 saniye bütçe ve her zaman idempotency anahtarıyla. Anahtarsız alıcı banka
çağrısı asla yeniden denenmez.

## Devre kesici

Dış bağımlılıklara giden istemciler, 30 saniyelik pencerede (en az 20 istek)
%50 hata oranından sonra açılan ve 15 saniye sonra yarı açılan bir devre
kesiciyle sarılır. Kesici açıkken `dependency_unavailable` ile hızlı hata
verilir; kuyruğa alınmaz.

## Zaman aşımları

Her dış çağrının açık bir zaman aşımı vardır. Varsayılanlar: iç servisler için
2 s, alıcı bankalar için 10 s, toplu dosya aktarımları için 30 s. Zaman aşımları
`context.Context` ile yayılır; kütüphane varsayılanlarına güvenilmez.
