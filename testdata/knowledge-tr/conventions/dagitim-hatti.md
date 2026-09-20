---
title: CI/CD hattı
tags: [ci, cd, dagitim]
---
# CI/CD hattı

## Aşamalar

1. **Lint ve birim testleri** her push'ta, 5 dakikanın altında.
2. **Entegrasyon testleri** dokunulan servisler için Docker bağımlılıklarına karşı.
3. **İmaj derleme ve imzalama**; imaj özeti dağıtım manifestine yazılır.
4. `main`'e merge'de **staging**'e otomatik dağıtım.
5. **Production** dağıtımı sürüm etiketiyle tetiklenir, canary aşamasından
   geçer ve uçtan uca duman testi gerektirir.

## Dağıtım pencereleri

Para taşıyan servislerin production dağıtımları ekibin saat diliminde
Pazartesi–Perşembe 09:00–16:00 arasında yapılır; 00:00 UTC mutabakat penceresi
ve 06:00 hakediş çalıştırması sırasında asla yapılmaz. Acil düzeltmeler olay
komutanı onayıyla muaftır.

## Geri alma

Her dağıtım önceki imajı hazır tutar; geri alma tek komuttur ve staging'de
haftalık olarak prova edilir.
