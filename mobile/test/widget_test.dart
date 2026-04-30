import 'package:flutter_test/flutter_test.dart';

import 'package:gatepilot_mobile/main.dart';

void main() {
  testWidgets('默认展示中文审批入口', (WidgetTester tester) async {
    // 验证移动端默认中文入口，避免后续国际化改动破坏默认语言要求。
    await tester.pumpWidget(const GatePilotMobileApp());

    expect(find.text('审批'), findsOneWidget);
    expect(find.text('设备'), findsOneWidget);
    expect(find.text('会话'), findsOneWidget);
  });

  testWidgets('支持切换英文', (WidgetTester tester) async {
    await tester.pumpWidget(const GatePilotMobileApp());

    await tester.tap(find.text('EN'));
    await tester.pump();

    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Devices'), findsOneWidget);
    expect(find.text('Sessions'), findsOneWidget);
  });
}
