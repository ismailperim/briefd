---
title: İade kuralları
tags: [odeme, iade, uyum]
---
# İade kuralları

Bu kurallar iade başlatabilen her yüzey için geçerlidir: üye işyeri paneli,
açık API ve destek araçları.

## İade süresi

İade, tahsilat tarihinden itibaren en fazla **180 gün** içinde talep
edilebilir. Bu sürenin dışındaki talepler `refund_window_expired` hatasıyla
reddedilir. Kart şemaları itirazlar için 180 güne kadar izin verdiğinden, bu
süreden sonra yapılacak bir iade daha sonraki bir ters ibrazla mutabakatı
imkânsız kılar.

Kısmi iadeye izin verilir; bir ödemedeki iadelerin toplamı tahsil edilen
tutarı asla aşamaz. Aşma girişimi `refund_exceeds_capture` döndürür.

## Onay eşikleri

| Tutar (iade başına) | Gerekli onay |
|---|---|
| ≤ 500 EUR karşılığı | yok — üye işyeri kendisi yapar |
| 500 – 5.000 EUR | `refunds:approve` rolüne sahip ikinci bir üye işyeri kullanıcısı |
| > 5.000 EUR | üye işyeri onayı **ve** Kestrel Pay risk ekibi |

Eşikler, 00:00 UTC'de alınan günlük kur anlık görüntüsüyle EUR cinsinden
değerlendirilir. Kontrol, talep anında yapılır; işlem anında değil.

## İtirazlı ödemelerde iade

Bir ödemenin açık ters ibrazı varsa iade `payment_disputed` ile engellenir.
İtiraz kaybedilirse fonlar iki kez geri dönmüş olur. İtiraz üye işyeri lehine
kapandığında normal süre içinde iade yeniden mümkündür.

## İade ve hakediş

İadeler üye işyerinin kullanılabilir bakiyesinden anında düşülür. Bakiye
yetersizse iade yine kabul edilir; bakiye eksiye düşer ve pozitife dönene kadar
sonraki hakediş azaltılır ya da atlanır. Üye işyeri bunu panelde
`pending_balance_recovery` işaretiyle görür.
