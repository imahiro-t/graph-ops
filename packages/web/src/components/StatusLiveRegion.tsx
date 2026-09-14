// 状態メッセージ（保存結果・起動失敗など、フォーカスを受け取らずに現れる通知）を
// 支援技術に伝えるための live region。WCAG 2.2 SC 4.1.3 (Status Messages) は、
// この種のメッセージに「プログラム的に決定可能な role」を要求する。
//
// なぜ「常時マウントされた sr-only の器」なのか:
//
//   1. live region は、中身が変わる前から DOM に存在している必要がある。
//      role="status" を持つ要素そのものを条件付きマウントすると、要素の挿入と
//      内容の出現が同時になり、読み上げられるかどうかがブラウザ／スクリーン
//      リーダーの組み合わせに依存する。器だけ先に置いておけば、変化するのは
//      テキストノードだけになり確実に通知される。
//   2. sr-only は position:absolute なので、空のときも flex の gap や
//      space-y の margin を消費しない。呼び出し側の既存レイアウトを 1px も
//      動かさずに live region を足せる。
//
// 見た目を持つ側の要素には aria-hidden="true" を付け、同じ文言が支援技術に
// 二重に現れないようにする（読み上げはこの器が担当する）。
import React from 'react';

interface Props {
  // 空文字は「通知なし」を表す。器は空のまま描画され続ける。
  message: string;
}

export const StatusLiveRegion: React.FC<Props> = ({ message }) => (
  <span role="status" aria-live="polite" className="sr-only">
    {message}
  </span>
);
