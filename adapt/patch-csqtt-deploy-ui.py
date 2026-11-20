#!/usr/bin/env python3
"""Patch the pinned LaLune Flutter deploy form with the CSQTT 2.1.9 auth contract."""
from pathlib import Path

ROOT = Path("upstream-lalune")
PAGE = ROOT / "Frontend/Core/lib/pages/deploy_page.dart"


def replace_once(old: str, new: str) -> None:
    source = PAGE.read_text()
    count = source.count(old)
    if count != 1:
        raise SystemExit(f"expected one UI marker in {PAGE}, found {count}: {old[:100]!r}")
    PAGE.write_text(source.replace(old, new, 1))


replace_once(
    "  final TextEditingController _password = TextEditingController();",
    """  final TextEditingController _password = TextEditingController();
  final TextEditingController _mainPassword = TextEditingController();
  final TextEditingController _webUser = TextEditingController(text: 'admin');
  final TextEditingController _webPassword = TextEditingController();""",
)
replace_once(
    "    _password.dispose();",
    """    _password.dispose();
    _mainPassword.dispose();
    _webUser.dispose();
    _webPassword.dispose();""",
)
replace_once(
    """    if (!_usePassword && _keyPath.text.trim().isEmpty) {
      _showToast('Укажите путь к SSH-ключу');
      return;
    }

    final payload = <String, dynamic>{""",
    """    if (!_usePassword && _keyPath.text.trim().isEmpty) {
      _showToast('Укажите путь к SSH-ключу');
      return;
    }

    final deviceId = Api.getDeviceId().trim();
    if (_protocol == 'CSQTT') {
      final secretPattern = RegExp(r'^[A-Za-z0-9]+$');
      if (!secretPattern.hasMatch(_mainPassword.text) ||
          !secretPattern.hasMatch(_webUser.text.trim()) ||
          !secretPattern.hasMatch(_webPassword.text)) {
        _showToast('Для CSQTT задайте пароль туннеля, логин и пароль WEB латиницей и цифрами');
        return;
      }
      if (deviceId.isEmpty || !RegExp(r'^[A-Za-z0-9-]+$').hasMatch(deviceId)) {
        _showToast('Не удалось получить корректный ID устройства LaLune');
        return;
      }
    }

    final payload = <String, dynamic>{""",
)
replace_once(
    """      'listenPort': int.tryParse(_listenPort.text.trim()) ?? 0,
    };

    setState(() => _deploying = true);""",
    """      'listenPort': int.tryParse(_listenPort.text.trim()) ?? 0,
    };
    if (_protocol == 'CSQTT') {
      payload.addAll({
        'mainPassword': _mainPassword.text,
        'webUser': _webUser.text.trim(),
        'webPassword': _webPassword.text,
        'deviceId': deviceId,
      });
    }

    setState(() => _deploying = true);""",
)
replace_once(
    """              _block('Порты', [""",
    """              if (_protocol == 'CSQTT')
                _block('Авторизация сервера CSQTT', [
                  TextField(
                    controller: _mainPassword,
                    enabled: !_deploying,
                    obscureText: true,
                    decoration: _inputDecoration('Пароль туннеля CSQTT (латиница и цифры)'),
                  ),
                  const SizedBox(height: 10),
                  TextField(
                    controller: _webUser,
                    enabled: !_deploying,
                    decoration: _inputDecoration('Логин веб-панели'),
                  ),
                  const SizedBox(height: 10),
                  TextField(
                    controller: _webPassword,
                    enabled: !_deploying,
                    obscureText: true,
                    decoration: _inputDecoration('Пароль веб-панели (латиница и цифры)'),
                  ),
                  const SizedBox(height: 6),
                  const Text(
                    'ID устройства берётся из настроек LaLune и привязывается к основному паролю, как в CSQTT 2.1.9.',
                    style: TextStyle(fontSize: 12, color: Colors.white70),
                  ),
                ]),

              _block('Порты', [""",
)
print("CSQTT Deploy UI fields and payload patched")
