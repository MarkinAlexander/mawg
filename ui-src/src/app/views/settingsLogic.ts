// Чистая логика карточки «Настройки» - порт функций легаси panel.js
// (ipValid 2584, subnet24 2595, lanSubnet24 2553, panelTargetOrigin 2764)
// для юнит-тестов без DOM.

// IP или подсеть CIDRv4: 4 октета <=255, маска /8../../32 (легаси ipValid)
export function ipValid(v: string): boolean {
  const cidr = v.split('/');
  if (cidr.length > 2) return false;
  const parts = cidr[0].split('.');
  if (parts.length !== 4) return false;
  for (const p of parts) if (!/^\d{1,3}$/.test(p) || +p > 255) return false;
  if (cidr.length === 2) {
    if (!/^\d{1,2}$/.test(cidr[1]) || +cidr[1] < 8 || +cidr[1] > 32) return false;
  }
  return true;
}

// «жадный» срез хвоста для СРАВНЕНИЯ префиксов: 1.2.3.4 -> 1.2, 1.2.3/24 -> 1.2.
// Для сборки подсети не годится (грабля 0fee1b4) - только сравнение.
export function subnet24(v: string): string {
  return v.replace(/\.\d+(\.\d+)?(\/\d+)?$/, '');
}

// честный /24 адреса клиента: IPv4-квадом, иначе '' (IPv6 и прочее)
export function lanSubnet24(clientIp: string | undefined): string {
  const mine = clientIp;
  if (!mine) return '';
  const p = String(mine).split('.');
  if (p.length !== 4 || p.some((x) => !/^\d{1,3}$/.test(x) || +x > 255)) return '';
  return p.slice(0, 3).join('.') + '.0/24';
}

// куда переедет панель по полям адрес/порт; null - остаётся на этом же origin
export function panelTargetOrigin(
  addrValue: string,
  portValue: number,
  location: { protocol: string; hostname: string; origin: string },
): string | null {
  const port = portValue || 8090;
  const addr = addrValue.trim() || '0.0.0.0';
  let host = location.hostname;
  if (addr !== '0.0.0.0' && addr !== '::' && addr !== location.hostname) host = addr;
  const target = location.protocol + '//' + host + ':' + port;
  return target === location.origin ? null : target;
}
