---
title: İtiraz ve ters ibraz
tags: [odeme, itiraz, ters-ibraz, uyum]
---
# İtiraz ve ters ibraz

## Yaşam döngüsü

İtiraz, ihraççı bankanın kart şeması üzerinden ters ibraz göndermesiyle
başlar. `dispute.opened` olarak, şema neden kodu, itiraz edilen tutar ve
**kanıt için son tarih** ile kaydedilir. Kanıt sunulunca `under_review`'a geçer;
şema karar verince `won`, `lost` ya da `withdrawn` ile kapanır.

## Kanıt süreleri

Kanıt, şemanın son tarihinden önce sunulmalıdır; bu süre ters ibrazın
alınmasından itibaren Visa için tipik olarak **7 gün**, Mastercard için
**10 gündür**. Kesin tarih her zaman şema mesajından alınır, yerelde
hesaplanmaz. Üye işyeri son tarihi panelde görür ve 48 saat önce hatırlatma
alır. Süre sonrası yüklenen kanıt saklanır ama iletilmez.

## Fonların tutulması

İtiraz açıldığında itiraz edilen tutar üye işyerinin kullanılabilir
bakiyesinden `disputed` blokesine taşınır ve ters ibraz ücreti hemen tahsil
edilir. İtiraz kazanılırsa bloke kalkar; kaybedilirse kalıcı olarak düşülür.
Ücret kazanılsa bile iade edilmez.

## Neden kodları

Neden kodları beş aileye indirgenir: `fraud`, `authorization`,
`processing_error`, `consumer_dispute`, `other`. Ham şema kodu kanıt şablonları
için saklanır. 3-D Secure kullanılan ödemelerdeki dolandırıcılık itirazları
`liability_shifted` olarak işaretlenir ve genellikle üye işyeri müdahalesi
olmadan kazanılır.
