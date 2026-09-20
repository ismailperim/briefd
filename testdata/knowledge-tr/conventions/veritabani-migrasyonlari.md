---
title: Veritabanı migrasyonları
tags: [veritabani, migrasyon, postgres]
---
# Veritabanı migrasyonları

## Araç

Migrasyonlar `migrations/` altında `NNNN_aciklama.sql` adlı düz SQL
dosyalarıdır; servis başlangıçta (Kubernetes pre-install hook) danışma
kilidiyle uygular, böylece replikalar yarışmaz.

## Geriye uyumluluk

Her migrasyon, önceki sürüm hâlâ çalışırken işlemelidir: sütunlar nullable ya
da varsayılanlı eklenir; kullanımı bırakılan sürümde sütun yeniden
adlandırılmaz veya silinmez. Bir sütunu kaldırmak **üç sürüm** alır: yazmayı
bırak, okumayı bırak, sil.

## Büyük tablolar

Bir milyondan fazla satırlı tabloya indeks eklerken işlem dışında
`CREATE INDEX CONCURRENTLY` kullanılır. Geri doldurmalar 10.000 satırlık
partiler halinde, aralarda bekleyerek ve kaldığı yerden devam edebilir şekilde
çalışır.

## Geri alma

Migrasyonlar geri alınmaz; hatalı migrasyon yeni bir migrasyonla ileriye doğru
düzeltilir. Uyumluluk kuralı sayesinde uygulama yine de geri alınabilir.
