package database

import "strings"

// tagTaxonomy 是 2026-09-28 对标站 /tag 全部标签的名称到类型映射；保留原名，不推测未知标签。
// 以分号分隔，避免名称自身的逗号被当成字典分隔符。分类展示统一简体中文。
var tagTaxonomy = map[string]string{
	"主题": "出軌;獵豔;企畫;M男;流汗;美容院;戀腿癖;其他戀物癖;魔鬼系;性感的;女同接吻;偷窺;倒追;亂倫;戀乳癖;白天出軌;女同性戀;妄想;惡作劇;跳舞;情侶;淫亂真實;暗黑系;強姦;洗澡;學校作品;運動;性騷擾;處男;正太控;戀物癖;蠻橫嬌羞;溫泉;處女;性愛;奴隸;爛醉如泥的;殘忍畫面;曬黑;雙性人;全裸;觸手;正常;奇異的;性轉換·女體化;男同性戀;韓國;形象俱樂部;友誼;亞洲;天賦;被外國人幹;刺青紋身;黑白配;絕頂高潮;純欲;經歷告白;濕身",
	"角色": "姐姐;已婚婦女;美少女;高中女生;蕩婦;辣妹;角色扮演者;偶像;妹妹;各種職業;女大學生;女教師;白人;童年朋友;婆婆;母親;妓女;大小姐;秘書;女主播;講師;賽車女郎;黑人演員;藝人;新娘，年輕妻子;明星臉;護士;家教;寡婦;女醫生;老闆娘，女主人;其他學生;模特兒;格鬥家;展場女孩;禮儀小姐;女檢察官;服務生;伴侶;車掌小姐;女兒;年輕女孩;公主;飛特族;亞洲女演員;痴漢;御宅族;老太婆;老年男性;拉拉隊;媽媽的朋友;養女;女王",
	"服装": "OL;制服;角色扮演;連褲襪;內衣;浴衣;校服;空中小姐;短裙;高跟鞋;水手服;娃娃;兔女郎;泳裝;學校泳裝;迷你裙;眼鏡;女忍者;猥褻穿著;運動短褲;修女;和服，喪服;女傭;女戰士;制服外套;裸體圍裙;身體意識;貓耳女;緊身衣;蘿莉角色扮演;女裝人妖;絲襪、過膝襪;泡泡襪;旗袍;女祭司;動畫人物;迷你裙警察;COSPLAY服飾;靴子",
	"体型": "巨乳;美乳;熟女;美臀;大屁股;苗條;高挑;無毛;超乳;瘦小身型;平胸;肌肉;胖女人;巨大陰莖;蘿莉塔;素人;孕婦;變性者;美腳;多毛",
	"行为": "中出;潮吹;多P;口交;乳交;女上位;深喉;吞精;接吻;自慰;顏射;放尿;按摩;足交;手淫;濫交;手指插入;淫語;69;舔陰;肛門・肛交;拳交;母乳;飲尿;騎乗位;排便;食糞;剃毛;二穴同入;兩女一男;兩男兩女;兩男一女;打屁股;約會;不穿內褲;不穿胸罩;後入;瑜伽·健身;白眼失神;搔癢",
	"玩法": "玩具;乳液;凌辱;拘束;SM;按摩棒;立即口交;羞恥;女優按摩棒;藥物;監禁;輪姦;戶外;跳蛋;汽車性愛;捆綁;緊縛;調教;插入異物;灌腸;露出;催眠;鴨嘴;糞便;脫衣;子宮頸;導尿;蒙面・面罩;唾液敷面;乳釘、穿孔、乳環;口球;輔助自慰;夫妻交換;假陽具;鼻鉤;蠟燭;站立後入",
	"类别": "業餘;單體作品;4K;素人作品;首次亮相;第一人稱攝影;4小時以上作品;VR;戲劇;介紹影片;數位馬賽克;美少女電影;主觀視角;局部特寫;投稿;紀錄片;薄馬賽克;故事集;給女性觀眾;獨立製作;滑稽模仿;感官作品;共演;經典;戀愛;感謝祭;無碼流出;無碼破解;綜藝;精選綜合;國外進口;成人電影;去背影片;戰鬥行動;特效;16小時以上作品;重印版;歷史劇;寫真偶像;3D;原作改編;訪問;教學;恐怖;西洋片;科幻;行動;綜合短篇;男性;冒險;模擬;愛好，文化;懸疑;R-15;觸摸打字;HDTV;心理驚悚;養尊處優",
}

// tagSubscriptionMigration 新建分类字典、订阅规则与幂等处理台账；不回填或更改历史影片。
// name 为标签原名，category 为七类之一；limit_date 可空且默认 NULL，表示未订阅。
// processed_at 为 UTC 时间文本，记录标签已处理的影片，取消影片订阅后不自动重建。
// 回退时保留新增表；删除它们会丢失订阅意图与去重依据，不提供破坏性回退。
func tagSubscriptionMigration(d Dialect) Migration {
	stmts := []string{
		"CREATE TABLE tag_catalog (name VARCHAR(255) NOT NULL PRIMARY KEY, category VARCHAR(16) NOT NULL)",
		"CREATE TABLE tag_subscriptions (name VARCHAR(255) NOT NULL PRIMARY KEY, limit_date VARCHAR(10) DEFAULT NULL, updated_at VARCHAR(40) NOT NULL)",
		"CREATE TABLE tag_subscription_matches (tag_name VARCHAR(255) NOT NULL, media_id VARCHAR(64) NOT NULL, processed_at VARCHAR(40) NOT NULL, PRIMARY KEY(tag_name,media_id), FOREIGN KEY(media_id) REFERENCES media(id))",
		"CREATE INDEX idx_tag_matches_media ON tag_subscription_matches(media_id)",
	}
	for _, category := range []string{"主题", "角色", "服装", "体型", "行为", "玩法", "类别"} {
		for _, name := range strings.Split(tagTaxonomy[category], ";") {
			stmts = append(stmts, "INSERT INTO tag_catalog(name,category) VALUES("+sqlLiteral(name)+","+sqlLiteral(category)+")")
		}
	}
	return Migration{Version: 20, Name: "tag_subscriptions", Statements: stmts}
}
