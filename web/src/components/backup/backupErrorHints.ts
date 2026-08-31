/** Strip restic JSON noise from job error strings for human display. */
export function humanizeJobError(message: string): string {
  if (!message) return ''
  const trimmed = message.trim()
  const jsonIdx = trimmed.indexOf('{"message_type"')
  let head = jsonIdx > 0 ? trimmed.slice(0, jsonIdx).trim() : trimmed
  if (head.length > 280) {
    head = head.slice(0, 280) + '…'
  }
  return head
}

/** Recovery hints for backup / Drive / job errors (customer-facing Turkish). */
export function backupOperationHint(message: string): string {
  const m = message.toLowerCase()
  if (m.includes('backup blocked') && (m.includes('free space') || m.includes('source data requires'))) {
    return 'Yedek diski için yeterli çalışma alanı yok — “Yedek Oluştur” ekranında yalnız gerekli siteleri seçin, sadece veritabanı yedeği alın veya hata mesajındaki gerekli boş alanı sağlayın.'
  }
  if (m.includes('panel yeniden başlatıldı') || m.includes('yanıt vermiyor') || m.includes('kullanıcı tarafından kapatıldı')) {
    return 'Yarım kalan işlem — yeni snapshot veya yedek oluşturarak devam edin.'
  }
  if (m.includes('read-only file system') || m.includes('/root/.cache/restic')) {
    return 'Restic önbelleği yazılamadı — panel güncellemesi sonrası snapshot\'ı yeniden başlatın (cache artık data/ altında).'
  }
  if (m.includes('ciphertext verification failed') || m.includes('config or key')) {
    return 'Drive\'daki restic reposu bozuk veya şifre uyuşmuyor — repo temizlenip yeni snapshot gerekir (destek).'
  }
  if (m.includes('unauthorized_client') || m.includes('couldn\'t fetch token')) {
    return 'Google Drive OAuth süresi doldu — GDrive bölümünden yeniden bağlanın.'
  }
  if (m.includes('500 internal server error') || m.includes('couldn\'t list directory')) {
    return 'Google Drive API geçici hata veya token sorunu — GDrive\'da yeniden bağlanın, birkaç dakika bekleyip snapshot\'ı tekrar deneyin.'
  }
  if (m.includes('role "root"') || m.includes('peer authentication')) {
    return 'PostgreSQL yedekleme root ile çalışmaz. Panel sudo -u postgres kullanır — veritabanı yedeğini yeniden oluşturun.'
  }
  if (m.includes('sudo') && m.includes('postgres')) {
    return 'sudo -u postgres izni gerekli. Sunucuda root için NOPASSWD sudoers kuralını kontrol edin veya HSERVER_PG_RUN_AS ayarlayın.'
  }
  if (m.includes('rclone check') || m.includes('is a file not a directory')) {
    return 'Drive doğrulama hatası — yedeği yeniden yükleyin (boyut karşılaştırması ile doğrulanır).'
  }
  if (m.includes('too small') || m.includes('database backup too small')) {
    return 'Yedek dosyası boş veya yarım kalmış. İşlem bitmeden yükleme yapılmamalı — yedeği silip yeniden oluşturun.'
  }
  if (m.includes('size mismatch')) {
    return 'Yerel ve Drive boyutları uyuşmuyor. Drive\'daki kopyayı silip yeniden yükleyin.'
  }
  if (m.includes('no refresh token')) {
    return 'Google hesabınızda myaccount.google.com/permissions → Heyserver erişimini kaldırın, panelden tekrar bağlanın; izin ekranında tüm kutuları onaylayın.'
  }
  if (m.includes('not connected') || m.includes('oauth')) {
    return 'Google Drive bağlantısını kesip OAuth ile yeniden bağlanın.'
  }
  if (m.includes('timeout') || m.includes('deadline')) {
    return 'İşlem zaman aşımına uğradı — daha küçük kapsam (sadece DB) veya daha uzun timeout deneyin.'
  }
  return 'İşlemi tekrar deneyin; sürerse teknik logu inceleyin veya destekle iletişime geçin.'
}

export function invalidBackupHint(type: string, size: number): string {
  if (type === 'database') {
    return `${size} B — pg_dump başarısız olmuş (genelde peer auth). Bu dosyayı silip veritabanı yedeğini yeniden oluşturun.`
  }
  return `${size} B — arşiv boş veya yarım kalmış. Silip tam yedek bitince tekrar deneyin.`
}

/** @deprecated use backupOperationHint */
export const gdriveErrorHint = backupOperationHint
