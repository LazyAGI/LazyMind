import { BellOutlined, WechatOutlined, TeamOutlined } from '@ant-design/icons';
import type { ChannelName } from './api';
export default function ChannelBrand({ channel, avatar }: { channel: ChannelName; avatar?: string | null }) {
  const safeAvatar = avatar && /^https:\/\//i.test(avatar) ? avatar : undefined;
  return <span className={`notification-brand is-${channel}`} aria-hidden="true">
    {safeAvatar ? <img src={safeAvatar} alt="" referrerPolicy="no-referrer" onError={e => { e.currentTarget.style.display = 'none'; }} /> : null}
    {channel === 'desktop' ? <BellOutlined /> : channel === 'feishu' ? <img src="/feishu-official.svg" alt="" /> : channel === 'wecom' ? <TeamOutlined /> : <WechatOutlined />}
  </span>;
}
