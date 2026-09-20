---
title: Sözlük
tags: [domain, terminoloji]
---
# Sözlük

Kestrel Pay projelerinde ortak kullanılan terimler. Bir proje dokümanıyla
çelişirse bu dosya geçerlidir; değişiklik için öneri açın.

## Taraflar

### Üye işyeri

Kestrel Pay üzerinden ödeme kabul eden işletme. `merchant_id` (ULID) ile
tanımlanır. Bir üye işyeri tam olarak bir **organizasyona** bağlıdır ve birden
çok **mağazası** olabilir. Kodda ve API'de İngilizce karşılığı `merchant`
kullanılır.

### Alıcı banka (acquirer)

Üye işyeri adına kart işlemlerini işleyen ve kart şemalarından fonları alan
banka ya da finansal kuruluş. Birden çok alıcı bankayla entegreyiz; her
entegrasyon `defter-servisi` içindeki `Acquirer` arayüzünün arkasındadır.

### Kart hamili

Kartla ödeme yapan son müşteri. Kart numarasını (PAN) asla saklamayız; yalnızca
ağ token'ı ve son dört hane tutulur.

## Para hareketleri

### Provizyon (authorization)

Kart hamilinin fonları üzerine konan bloke. Tahsil edilmezse **7 gün** sonra
düşer (şema varsayılanı; bazı alıcı bankalar 30 güne izin verir). Provizyon
para taşımaz ve defterde kayıt oluşturmaz.

### Tahsilat (capture)

Provizyonun gerçek bir ücrete dönüştürülmesi. Kısmi tahsilat mümkündür. İlk
tahsilat, defterde **ödeme** kaydını oluşturur.

### Mutabakat dönemi (settlement)

Alıcı bankanın tahsil edilen fonları Kestrel Pay'e aktarması. Mutabakat
partileri her gün **00:00 UTC**'de kesilir. Mutabakat **hakediş**ten farklıdır:
mutabakat içeri gelen para, hakediş üye işyerine giden paradır.

### Hakediş (payout)

Kestrel Pay'deki üye işyeri bakiyesinden üye işyerinin banka hesabına yapılan
transfer. Üye işyerinin hakediş takvimine göre (günlük, haftalık veya manuel)
çalışır.

### İade (refund)

Tahsil edilen fonların kart hamiline geri verilmesi. İade her zaman bir
ödemeye bağlıdır ve kısmi olabilir. Süre ve onay kuralları için iade kuralları
dokümanına bakın.

### Ters ibraz (chargeback)

Kart hamilinin bankası (ihraççı) tarafından kart şeması üzerinden başlatılan
zorunlu geri alma. Bir neden kodu ve itiraz için son tarih taşır; üye işyeri
kanıt sunabilir.

## Tanımlayıcılar

### Idempotency anahtarı

Kaynak oluşturan veya para taşıyan her istekle gönderilen, istemci tarafından
üretilmiş benzersiz dize. Aynı anahtarla tekrarlanan istek, yan etki olmadan
ilk sonucu döndürmelidir. Saklama süresi için idempotency kuralına bakın.
