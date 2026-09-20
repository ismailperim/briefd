---
title: Go stil rehberi
tags: [go, konvansiyon]
---
# Go stil rehberi

## Biçimlendirme ve lint

Tüm Go kodu `gofmt` ile biçimlendirilir ve `platform/lint/.golangci.yml`
yapılandırmasıyla `golangci-lint`'ten geçer. CI herhangi bir lint bulgusunda
başarısız olur; gerekçe yorumu olmadan `//nolint` eklenmez.

## Hatalar

Hataları bağlamla sarın: `fmt.Errorf("doing x for %s: %w", id, err)`. Sentinel
hatalar `ErrXxx` adlı dışa açık değişkenlerdir. Aynı hatayı hem loglayıp hem
döndürmeyin. Panik yalnızca başlangıçtaki programcı hataları içindir.

## Para

Para `platform/money` paketindeki `money.Amount{Minor int64, Currency string}`
ile temsil edilir. Tutarlar için asla `float64` kullanmayın ve `Amount`'ı
float'tan üretmeyin. Gösterim biçimlendirmesi yalnızca en dış katmanda yapılır.

## Test

`t.Run` ile tablo güdümlü testler. Zamana bağlı kod için enjekte edilmiş saat
kullanın; testlerde asla `time.Sleep` yok. Postgres gerektiren entegrasyon
testleri `//go:build integration` etiketiyle gece hattında çalışır.
