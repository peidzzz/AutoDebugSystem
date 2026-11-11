"""CRC32 as computed by boot.c (poly 0x04C11DB7, MSB first, init 0xFFFFFFFF,
final xor) so a .bin can be checked against the value printed by ymodem-ota."""
import sys


def crc32_stm32(data):
    table = []
    for nibble in range(16):
        value = nibble << 28
        for _ in range(4):
            value = ((value << 1) ^ 0x04C11DB7) & 0xFFFFFFFF if value & 0x80000000 \
                else (value << 1) & 0xFFFFFFFF
        table.append(value)

    crc = 0xFFFFFFFF
    for byte in data:
        crc = ((crc << 4) & 0xFFFFFFFF) ^ table[((crc >> 28) ^ (byte >> 4)) & 0x0F]
        crc = ((crc << 4) & 0xFFFFFFFF) ^ table[((crc >> 28) ^ (byte & 0x0F)) & 0x0F]
    return crc ^ 0xFFFFFFFF


for path in sys.argv[1:]:
    payload = open(path, 'rb').read()
    print('%-40s %7d bytes  crc32 0x%08X' %
          (path.replace('\\', '/').split('/')[-1], len(payload), crc32_stm32(payload)))
