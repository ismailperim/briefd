---
title: Idempotency
tags: [api, guvenilirlik, odeme]
---
# Idempotency

## Hangi istekler anahtar gerektirir

Kaynak oluşturan veya para taşıyan her istek `Idempotency-Key` başlığı taşımak
zorundadır: ödeme, tahsilat, iade, hakediş oluşturma ve üye işyeri kaydı. GET
istekleri anahtar kullanmaz. Anahtarsız gelen değiştirici istekler
`idempotency_key_required` ile reddedilir.

## Anahtar biçimi ve kapsamı

Anahtarlar istemcinin ürettiği 16–64 karakterlik dizelerdir. Gönderen API
anahtarına göre kapsamlanır; iki üye işyeri aynı değeri çakışmadan kullanabilir.
UUIDv4 ya da ULID öneririz.

## Tekrar (replay) davranışı

Aynı anahtar ve aynı gövdeyle tekrarlanan istekte ilk yanıt (durum kodu,
başlıklar, gövde) değişmeden döndürülür. Gövde farklıysa istek
`idempotency_key_reused` ve HTTP 422 ile reddedilir; eski sonucu sessizce
döndürmek istemci hatasını gizlerdi.

İlk istek hâlâ işlenirken gelen tekrar HTTP 409 `idempotency_in_progress`
döndürür; istemci `Retry-After` süresinden sonra yeniden denemelidir.

## Saklama

Idempotency kayıtları **24 saat** tutulur. Sonrasında anahtar yeniden
kullanılabilir ve yeni istek gibi işlenir. Daha uzun koruma isteyen istemciler
kendi tekilleştirmesine (örneğin `payment_id` saklayarak) güvenmelidir.
