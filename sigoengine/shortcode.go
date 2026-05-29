//**********************************************************************
//      sigoengine/shortcode.go
//**********************************************************************
//  Autor    : Gerhard Quell - gquell@skequell.de
//  CoAutor  : claude sonnet 4.6
//  Copyright: 2026 Gerhard Quell - SKEQuell
//  Erstellt : 20260513
//**********************************************************************
// Beschreibung: Sprechende Shortcode-Generierung für KI-Modelle
//               Strukturiertes Parsing: Familie + Version + Variante
//               Cutter-Sanborn für unbekannte Varianten
//               Kollisionsauflösung
//**********************************************************************

package sigoengine

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// **********************************************************************
// Cutter-Sanborn Präfix-Daten (A-Z, Pipe-separiert)
// Quelle: /u/ki-projekte/cuttercode/ (Bibliothek von Gerhard Quell)

var cutterPrefixData = []string{
	// A
	"a|ab|abd|abe|abr|ac|ach|achi|ack|acto|actor|acu|acul|acup|acur|acus|ada|adab|adap|add|ade|adi|adl|adm|ado|ador|adql|adr|aer|ag|agn|agr|ai|aid|aide|aiden|aides|aidi|aido|al|alb|ald|ale|alg|ali|alib|alibe|alibi|alibr|alic|all|ally|alr|alv|am|amb|ame|ami|amm|amo|ams|an|and|ang|ans|apl|apo|app|appl|apple|apr|apt|arb|ari|arn|arns|art|as|ase|ash|ashw|asl|ass|ast|asu|ath|atk|atl|au|aue|aur|aux|ave|awk|ay|aye|ayn|ays|az",
	// B
	"b|bac|bah|bal|ball|bap|bar|barn|bart|bas|bass|bast|bat|bato|bau|baue|baun|bax|bayn|bcpl|be|beb|bec|beg|bel|bell|belm|ben|benn|bep|ber|berg|bergi|berl|bern|bero|berr|bert|berto|bertr|bes|beu|bi|bie|bil|bio|bit|blag|blan|bli|blo|bo|boe|boh|boi|bol|bom|bon|bono|boo|bor|borr|bos|bot|bou|boui|bourc|bourg|bout|bow|boy|bra|bram|brar|bre|brem|bres|bri|brig|brit|bro|brou|brow|bru|brun|bruns|bu|buc|buf|bul|buo|bur|burg|burn|burr|bus|but|buy|bz",
	// C
	"c|cab|cabr|cad|cado|cae|cah|cai|caj|cal|cali|cam|can|cap|car|carm|cars|cas|cat|cdl|ce|cen|cer|cha|cham|champ|chan|chao|char|chart|chat|chaz|che|cher|chil|choi|chri|chu|cil|cir|cl|clar|clas|clav|cle|cler|cli|clo|co|cob|coc|cod|cog|col|cole|coli|coll|colo|colt|com|comb|come|comi|comm|comp|compa|compt|compu|coms|comsk|comy|con|conc|cond|cone|conf|cong|coni|conk|conl|coo|cor|cors|cot|cou|cr|cre|cro|cu|cur|cus|cush|cust|cut|cuv|cuy|cy|cyr|cz",
	// D
	"d|dae|dak|dal|dam|dan|dani|dany|darm|das|dass|dat|datae|dataf|datah|datal|datam|date|dato|datr|dau|daun|dav|dave|davi|davis|daw|dawk|day|db|dba|dbac|dbal|dban|dbao|dbar|dbas|dbat|dc|de|deb|dec|def|deg|dek|del|delb|deli|delo|delu|dem|demo|den|denn|deo|deq|der|des|dese|desi|desn|desr|det|dev|devo|dew|dewl|dh|di|dic|did|die|dil|dio|dir|dix|dmo|do|doe|dol|dom|don|doo|dor|dort|dou|dour|dow|drag|draw|drawe|drawp|du|dui|dum|dup|dw|dy|dz",
	// E
	"e|eac|eag|eal|ean|eap|ear|earm|eas|eb|eber|ec|ech|ed|eden|edet|edg|edis|edwa|edwe|ef|eg|ege|eger|egi|egl|ego|egr|egs|eha|ehab|ehad|ehai|ehal|ehau|ehbi|ein|eis|el|elac|elad|elag|elai|elal|elan|eli|ell|elli|elt|emm|en|enn|ep|eps|erd|ere|erg|erge|ergo|eri|erin|eris|erlg|err|ers|es|esd|esl|ess|est|este|esti|estr|et|eth|eto|etw|eu|eub|euc|eucl|eul|eup|eus|ev|eve|ewi|ewu|ex|exap|exav|exe|exen|exer|exl|exu|eye|eys|ez",
	// F
	"f|fab|fad|fai|fal|fan|far|fas|fat|fau|faul|faur|faus|fav|favr|fay|fayt|fe|fel|fen|fer|ferr|ferro|feu|fic|fie|fil|fin|fir|fis|fit|fl|fle|flex|flo|flop|flot|foc|foci|foco|focu|fod|foe|foed|fog|fol|font|fontm|foo|for|fore|form|foro|fors|fort|forte|forth|forti|forto|fortr|foru|fos|fosi|foss|fost|fou|foul|foun|four|fow|fox|fp|fram|fran|franc|frang|frank|franz|fras|fray|fre|free|frer|fri|fro|fron|fuc|ful|full|fum|fun|funk|fur|furn|furt|fus|fusu|fyr|fz",
	// G
	"g|gad|gai|gal|gall|galw|gan|gar|garn|gas|gast|gat|gau|gaul|gaus|gaut|gav|gay|ge|ged|geh|gel|gell|gem|gen|geni|geno|gent|geo|geol|ger|gere|gerr|gery|gf|gh|gi|gib|gic|gig|gil|gilp|gir|girt|gl|gle|gml|go|gog|gol|gon|goo|good|gor|gort|gott|goul|gour|gp|gr|graf|grah|gral|gram|gran|grand|grang|grant|granv|grap|graph|graphic|graphicg|graphico|graphicw|graphik|graphikl|graphikr|graphin|graphio|gras|gre|gree|grel|grev|grie|grim|gro|gru|gsx|gu|gue|gueu|gui|gum|gur|gut|gw|gz",
	// H
	"h|hac|had|hae|hag|hai|hal|hale|hall|halle|halo|ham|hami|hamm|han|hann|hanw|har|hard|hare|harl|harp|harr|harri|hart|harv|has|hass|hat|hau|haus|have|haw|hay|hayl|hdl|he|heb|hed|heg|hei|hel|helw|hen|henk|henr|her|herb|herg|herm|hero|hers|herv|hess|heus|hew|hey|hic|hig|hil|hin|hit|hl|ho|hod|hoe|hof|hol|hole|holl|holly|holo|hom|hon|hoo|hoop|hop|hor|hort|hos|hou|hov|how|howi|hoz|hu|hue|hug|hul|hum|hun|hunt|huo|hur|hut|hy|hyp|hypr|hz",
	// I
	"i|ib|ibi|ibm|ibn|ibr|ic|ice|ich|ick|ico|icon|id|ide|iden|ido|ie|if|ig|ih|ik|il|ili|ill|illu|im|imb|iml|imp|in|inb|inbr|inc|inch|ind|indi|indo|indu|ine|inef|inf|infe|infi|info|ing|inge|inger|ingh|ingi|ingl|ingli|ingo|ingr|ingre|ingri|inh|inhe|inhi|ini|inm|ino|inp|inq|ins|int|into|inv|inw|io|ip|ipr|iq|ire|iri|iro|irv|isa|isam|isc|ise|iset|isi|isl|ism|isn|iso|isq|isr|ist|isw|it|ito|its|iu|iv|ive|ivo|ix|iz",
	// J
	"j|jab|jac|jace|jack|jackr|jacks|jaco|jacob|jacop|jacq|jad|jadd|jado|jae|jaeg|jaf|jag|jagu|jah|jak|jal|jam|jame|jami|jan|jane|janu|jap|jaq|jaque|jar|jard|jas|jav|jb|jc|jd|je|jeb|jef|jeff|jek|jel|jem|jen|jenk|jenks|jenn|jer|jere|jero|jerv|jes|jesu|jet|jett|jev|jew|ji|jid|jo|joc|joe|joh|john|johns|joi|jok|jol|jole|joll|jom|jon|jons|joo|jop|jor|jord|jori|jork|joru|jos|joss|jou|joy|ju|jude|jun|juni|jur|juri|jus|jut|juv|juw|jux|jy|jz",
	// K
	"k|kad|kae|kag|kah|kai|kal|kam|kap|kar|kas|kat|kaw|kay|ke|kee|kef|keg|keh|kei|kel|kem|kemp|ken|kenn|keno|kens|kent|keo|kep|ker|kerk|kerr|ket|key|kh|ki|kil|kim|kin|king|kip|kir|kirk|kirs|kit|kitl|kl|kle|kli|klic|klim|klir|klo|km|kn|kne|kni|knig|knis|kno|knol|knop|know|knowl|ko|koc|koe|koen|koh|kon|kop|kor|kort|kos|kot|kou|kp|kr|kraj|kram|kran|krat|kre|krei|kri|kris|kro|kru|ks|kt|kuro|kurv|kus|kut|kv|kw|ky|kz",
	// L
	"l|lab|labo|lac|lach|laco|lady|lae|lago|lal|lam|lan|lane|lap|lar|las|lat|latr|laud|laur|lay|lb|lc|lcf|le|lee|lei|len|lep|let|lex|li|lin|lisp|lit|litt|liu|liv|livi|lk|ll|lm|lo|loc|lock|loco|lod|loe|lof|log|loge|logi|logl|logo|lok|lokr|lol|lom|lomb|lomo|lon|long|lor|lori|lort|los|loto|lotu|loug|lour|louv|lov|low|lowe|lown|loy|lp|lpl|lq|lr|ls|lsp|lt|lu|luc|lud|luk|lun|lus|lut|ly|lye|lym|lyn|lyr|lys|lyt|lytt|lz",
	// M
	"m|mab|mac|macd|mace|mach|mack|macl|maco|macp|macr|macs|macw|mad|maf|mag|mai|maj|mak|mal|mam|mand|mann|map|mapl|marg|mari|maro|mars|mart|marv|mas|masq|mast|mat|may|me|meg|mel|mer|mes|mev|mi|mil|mim|mimo|min|mino|mio|mir|mire|miro|mis|mit|mitt|mk|ml|mo|moc|mod|mode|modi|modu|moe|mof|mog|moh|moi|mok|mol|molo|mon|mons|mont|moo|mop|mor|moro|mos|mou|mous|mov|mu|mul|multi|multil|multim|multip|mum|mun|munf|munt|mup|mur|murr|musp|muto|my|mz",
	// N
	"n|nac|nad|nag|nai|nam|nan|nap|nar|narr|nas|nasm|nat|nath|natu|nau|naud|naue|naum|nav|navi|naw|nd|ne|neal|neav|nec|nee|neef|neg|nei|nel|nem|nep|ner|nes|net|netv|neuk|nev|nevi|new|newt|nic|nich|nico|nid|nie|nig|nil|nin|nit|no|nob|nod|nog|nok|nol|noo|nor|nori|norr|nort|north|norto|norw|nos|not|note|noti|nott|nou|nour|nov|nove|novel|novi|now|nox|noy|np|npl|nr|nro|nu|nug|nuk|nul|null|num|numm|nun|nur|nus|nut|nutt|nuv|ny|nz",
	// O
	"o|ob|obi|obr|obs|oc|occ|och|oco|oct|octo|od|ode|odi|odo|odon|odr|ods|oe|oer|of|off|ofl|og|ogl|oh|ohe|ohm|ohu|oi|ok|ol|olb|old|ole|oli|olip|oliv|olm|olo|oly|om|ome|omi|omn|omo|omu|on|one|onl|onp|ons|ont|op|opp|ops|or|orb|ord|ore|orf|org|ori|ork|orl|orm|orn|orne|oro|orp|orr|ors|ort|orto|orv|os|osg|osi|osk|osm|oso|osr|oss|ost|osw|ot|oth|ott|otw|ou|ous|ouv|ov|ow|ox|oxi|oxy|oy|oz",
	// P
	"p|pac|pad|page|pagem|pain|pal|pals|pant|par|parag|parg|park|parl|parlo|parq|parr|pars|part|pas|pasc|past|pat|pc|pcf|pcp|pcs|pct|pe|pel|pet|ph|pho|pi|pig|pil|pin|pio|pir|pis|pit|pl|plac|plan|plane|plann|plano|planp|plant|plas|plat|ple|plm|plo|po|poi|pol|pom|pon|pont|pop|popi|popp|por|pore|pori|porr|port|pos|pot|pou|pow|pr|pre|pri|prim|prio|pro|proc|prod|prodi|proe|prof|prog|progr|proj|prok|prol|prolo|pron|prop|pros|prosc|prot|ps|pu|pul|put|py",
	// Q
	"q|qa|qac|qad|qade|qae|qah|qai|qal|qao|qar|qat|qay|qb|qbe|qbi|qbo|qc|qd|qe|qec|qech|qee|qei|qen|qev|qf|qfi|qg|qh|qhi|qho|qi|qic|qie|qin|qk|ql|qm|qn|qo|qoc|qoe|qog|qoj|qol|qp|qpo|qr|qs|qsi|qt|qu|quac|quad|quak|qual|quan|quar|quart|quas|quasi|quast|quat|quata|quatr|quatt|quatu|qub|qube|qud|que|quec|qued|quel|quell|quen|queq|quer|ques|quet|qui|quic|quick|quid|quil|quin|quir|quis|quit|quo|quod|quol|quor|quu|qw|qx|qy|qz",
	// R
	"r|rac|rad|rae|rag|rai|rain|ram|ramo|ran|rand|rank|rapi|ras|rat|rau|rav|raw|ray|rb|re|reb|rec|red|redi|redu|ree|reg|regu|rei|reif|reil|rel|relp|rem|ren|reng|rens|rer|res|rest|reu|rev|rex|rf|rh|ri|rich|rid|ridg|rie|rig|rin|rio|ris|rit|riv|ro|rob|robe|roc|roch|rod|roe|rog|roh|rok|rol|roll|rom|rome|romu|ron|rond|roo|ror|ros|rose|rosn|ross|rot|roth|rou|row|rox|rp|rq|rsx|ru|rud|rug|run|runn|rur|rus|rut|rv|rx|rz",
	// S
	"s|sad|sal|sam|san|sas|sav|sc|sch|sche|schem|schl|schm|scho|schr|schri|schu|sci|sco|scp|scr|sd|se|sec|sed|see|seg|segr|sel|seq|ser|ses|set|sh|shi|si|sie|sig|sil|sim|simi|simo|simp|sims|simsc|simu|simup|sin|sir|sis|sit|siv|ski|sli|sm|smal|smar|smat|sme|smi|sml|smt|sn|sni|snm|sno|som|sou|sp|sq|st|star|stat|statg|ste|stel|step|stev|sto|stor|str|stu|su|sul|sum|summ|sun|sup|superc|superv|sur|sw|swi|sy|syl|sym|syp|sys|sz",
	// T
	"t|tad|tai|tal|tam|tan|tar|tarl|tas|tat|tav|tc|td|te|teg|tel|tem|ten|tenn|ter|terr|terw|tes|testo|tet|tetr|teu|tex|texi|text|texte|texti|textm|texto|textom|textor|th|the|theo|thes|thet|thi|thie|tho|thom|thor|thorn|thu|thy|ti|tig|tim|tin|tir|tis|tit|to|tod|tof|tok|tom|too|top|topas|tor|tos|tou|tp|tr|tre|tri|tro|tru|tu|tue|tur|turc|ture|turen|turg|turgo|turi|turm|turn|turne|turo|turt|tus|tut|tv|tw|twy|ty|tyn|typ|typo|tyr|tys|tz",
	// U
	"u|ub|ube|uber|ubi|uc|uch|ucs|ud|ude|udi|uds|ue|uf|uff|ug|ugo|uh|uhd|uhl|uhr|uht|ui|uk|ukr|ul|ule|ulf|uli|ull|ulm|ulo|ulp|ulr|uls|ult|um|umb|umd|umf|umk|uml|umo|umr|ums|un|und|unde|une|unf|ung|unh|uni|unif|unil|unio|uniq|unis|unit|univ|unix|unl|uno|unp|unr|uns|unt|uo|up|uph|ups|upt|ur|urb|urc|ure|uri|url|uro|urq|urr|urs|urv|us|ush|usl|uss|ust|ut|uti|utl|utr|utt|uv|uvo|uw|ux|uy|uz",
	// V
	"v|vac|vae|vaj|val|vald|vale|valh|vall|valo|vam|vamo|van|vand|vang|vann|vans|var|vari|varn|vas|vash|vat|vau|vaug|vaut|vax|vc|vd|ve|ved|vee|veh|vei|vel|vell|ven|vend|vene|veni|vent|ventu|vep|ver|verb|verc|verd|verdo|vere|verel|verg|vergi|vergo|verh|veril|verk|vero|verr|ves|vet|vh|vi|vic|vie|vig|vil|vill|vim|vin|vino|vip|vir|virl|vis|visi|vism|vit|vite|viter|vitr|vitt|viv|vivi|vivo|viz|vo|vog|voi|vol|volk|voln|volt|von|vor|vos|vr|vu|vul|vz",
	// W
	"w|wad|wag|wak|wal|wale|walk|wall|walr|walt|wan|war|ward|warh|warn|warr|was|wat|wate|watk|wau|we|wee|wel|wen|wes|wey|wh|whi|wi|wif|wil|win|wind|wine|wing|wink|wins|wint|wip|wir|wis|wist|wit|witc|wite|wo|wog|wol|wolt|woo|wood|woodm|wool|woor|wop|wor|word|wordc|worde|wordf|wordi|wordl|wordm|wordn|wordo|wordp|wordpr|wordr|words|wordst|wordt|wordu|wordw|wordx|wore|worh|work|wors|wort|wot|wou|wr|wre|wri|wrig|wris|writ|write|writen|writl|wu|wun|wy|wye|wyl|wyn|wyt|wz",
	// X
	"x|xad|xah|xai|xak|xal|xale|xam|xamo|xan|xane|xann|xano|xant|xao|xap|xar|xas|xau|xav|xave|xaw|xay|xb|xbe|xc|xd|xde|xdr|xe|xed|xeg|xei|xej|xel|xem|xen|xeno|xeo|xep|xer|xerc|xere|xeri|xerx|xes|xese|xesh|xeso|xest|xet|xeu|xeus|xew|xex|xey|xf|xg|xh|xi|xic|xie|xif|xil|xim|xio|xip|xis|xl|xm|xn|xo|xoc|xoe|xol|xon|xong|xor|xot|xou|xp|xr|xs|xt|xu|xuc|xun|xur|xy|xyc|xyk|xyl|xym|xyp|xyr|xys|xyt|xyw|xz",
	// Y
	"y|yac|yad|yag|yah|yahc|yak|yal|yale|yam|yan|yane|yani|yann|yant|yao|yap|yar|yard|yarf|yari|yark|yarn|yaro|yarr|yas|yasm|yat|yatm|yb|yd|ye|yead|yeah|yeal|yeap|year|yeas|yeat|yeb|yef|yek|yel|yen|yeo|yep|yes|yest|yet|yez|yi|yl|yn|yo|yoc|yoe|yog|yoh|yol|yon|yond|yone|yong|yonge|yono|yons|yop|yor|yori|york|yorke|yos|yose|yosi|yot|yott|you|youn|young|youp|your|yous|yov|yoz|yp|yr|yri|ys|yse|yss|yu|yul|yv|yve|yves|yvo|yvu|yx|yz",
	// Z
	"z|zab|zac|zacc|zach|zack|zacu|zae|zaf|zag|zagh|zah|zai|zak|zal|zam|zamb|zamo|zamp|zan|zand|zane|zanf|zang|zank|zann|zano|zant|zap|zar|zari|zaro|zas|zast|zat|zau|zaun|zb|ze|zec|zed|zeg|zei|zeif|zeis|zeit|zeitk|zej|zek|zel|zelo|zelt|zen|zeno|zens|zent|zeo|zep|zepp|zer|zeri|zero|zes|zet|zeu|zev|zi|zie|zieg|zies|zif|zig|zigl|zik|zil|zill|zim|zimm|zin|zino|zins|zio|zip|zis|zit|ziv|zl|zo|zoe|zol|zop|zot|zu|zuc|zun|zur|zw|zwi|zy",
}

// **********************************************************************
// Lazy-initialisierte Cutter-Prefix-Arrays

var (
	cutterArrays [][]string
	cutterOnce   sync.Once
)

// initCutter initialisiert die Prefix-Arrays beim ersten Zugriff
func initCutter() {
	cutterArrays = make([][]string, 26)
	for i, line := range cutterPrefixData {
		cutterArrays[i] = strings.Split(line, "|")
	}
}

// **********************************************************************
// Familien-Prefixe (sortiert nach Länge, längster Match zuerst)
// Reihenfolge wichtig: "text-embedding" vor "text", "deepseek-r" vor "deepseek"

type familyEntry struct {
	prefix  string
	short   string
}

var familyPrefixes = []familyEntry{
	{"text-embedding", "emb"},
	{"deepseek-r", "dsr"},
	{"deepseek", "ds"},
	{"moonshot", "moon"},
	{"minimax", "mmx"},
	{"codestral", "cstr"},
	{"devstral", "dvstr"},
	{"mistral", "mist"},
	{"claude", "cl"},
	{"gemini", "gem"},
	{"qwen", "qwen"},
	{"llama", "llama"},
	{"grok", "grok"},
	{"kimi", "kimi"},
	{"glm", "glm"},
	{"gpt", "gpt"},
	{"sonar", "son"},
	{"ollama", "ollama"},
}

// **********************************************************************
// Bekannte Varianten-Abkürzungen

var variantMap = map[string]string{
	"mini":       "m",
	"nano":       "n",
	"micro":      "u",
	"small":      "s",
	"large":      "l",
	"flash":      "f",
	"pro":        "p",
	"lite":       "lt",
	"air":        "a",
	"turbo":      "t",
	"vision":     "vis",
	"image":      "img",
	"chat":       "ch",
	"code":       "cod",
	"codex":      "cx",
	"fast":       "fst",
	"thinking":   "thnk",
	"reasoning":  "reas",
	"preview":    "pv",
	"beta":       "b",
	"instruct":   "inst",
	"highspeed":  "hs",
	"terminus":   "term",
	"research":   "res",
	"customtools": "ct",
	"non":        "no",
	"plus":       "pl",
	"scout":      "sc",
	"maverick":   "mv",
	"sonnet":     "s",
	"opus":       "o",
	"haiku":      "h",
	"auto":       "auto",
	// Ignorierte Varianten (leerer String)
	"base":   "",
	"latest": "",
}

// **********************************************************************
// cutterCode ermittelt den Cutter-Sanborn-Code für ein Wort

func cutterCode(word string) string {
	cutterOnce.Do(initCutter)

	if len(word) == 0 {
		return ""
	}

	lower := strings.ToLower(word)
	first := strings.ToUpper(string(lower[0]))[0]

	// Buchstaben-Index: A=0, B=1, ...
	idx := int(first - 'A')
	if idx < 0 || idx >= 26 {
		// Fallback für Nicht-ASCII: erste 3 Zeichen
		if len(lower) > 3 {
			return lower[:3]
		}
		return lower
	}

	prefixes := cutterArrays[idx]

	// Beste Präfix-Übereinstimmung finden (längster Match)
	bestIdx := -1
	bestLen := 0
	for i, p := range prefixes {
		if strings.HasPrefix(lower, p) && len(p) > bestLen {
			bestIdx = i
			bestLen = len(p)
		}
	}

	if bestIdx < 0 {
		bestIdx = 0
	}

	// Code: Buchstabe + 2-stellige Zahl
	return fmt.Sprintf("%c%02d", first, bestIdx+1)
}

// **********************************************************************
// isDigitOnly prüft ob ein String nur aus Ziffern und Punkten besteht

func isDigitOnly(s string) bool {
	for _, c := range s {
		if c != '.' && (c < '0' || c > '9') {
			return false
		}
	}
	return len(s) > 0
}

// **********************************************************************
// isDigitish prüft ob ein String mit einer Ziffer beginnt
// und optional Buchstaben enthält ("4o", "8k", "128k")

func isDigitish(s string) bool {
	return len(s) > 0 && s[0] >= '0' && s[0] <= '9'
}

// **********************************************************************
// isParamSize erkennt Parameter-Größen wie "9b", "24b", "35b", "a3b", "a17b"

func isParamSize(s string) bool {
	if len(s) < 2 {
		return false
	}
	// Endet auf "b" und enthält Ziffern
	return strings.HasSuffix(s, "b") && !isDigitOnly(s[:len(s)-1])
}

// **********************************************************************
// GenerateShortcode erzeugt einen sprechenden Shortcode für ein Modell
//
// Algorithmus:
//  1. Familie erkennen (longest prefix match)
//  2. Version extrahieren (führende Zahlengruppe)
//  3. Varianten übersetzen (bekannt → Kürzel, unbekannt → Cutter)
//  4. Zusammenbauen: familie + version + "-" + varianten
//  5. Kollisionsauflösung mit numerischem Suffix

func GenerateShortcode(modelID string, used map[string]bool) string {
	mid := strings.ToLower(modelID)

	// 1. Familie finden (longest match)
	family := ""
	rest := mid
	for _, entry := range familyPrefixes {
		if strings.HasPrefix(mid, entry.prefix) {
			family = entry.short
			rest = mid[len(entry.prefix):]
			rest = strings.TrimPrefix(rest, "-")
			break
		}
	}

	if family == "" {
		// Keine bekannte Familie: Cutter auf ganzen Namen
		return strings.ToLower(cutterCode(modelID))
	}

	// 2. Parts splitten
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "-")
	}

	// 3. Struktur erkennen: [subfamily] [version] [variant...]
	// Subfamily = sonnet/opus/haiku etc. (kommen oft VOR der Version)
	// Version = Zifferngruppen
	// Variant = alles danach

	subfamily := ""
	version := ""
	remaining := []string{}
	parsingVersion := true

	for _, p := range parts {
		// Bekannter Varianten-Name
		if abbr, ok := variantMap[p]; ok {
			if subfamily == "" && version == "" && abbr != "" {
				// Vor der Version: Subfamily (sonnet, opus, haiku)
				subfamily = abbr
			} else {
				// Nach der Version: Variante
				parsingVersion = false
				if abbr != "" {
					remaining = append(remaining, p)
				}
			}
			continue
		}

		if !parsingVersion {
			remaining = append(remaining, p)
			continue
		}

		// Parameter-Größe ("9b", "24b") → beendet Versionsphase
		if isParamSize(p) {
			parsingVersion = false
			remaining = append(remaining, p)
			continue
		}

		// "v1" → Version "1"
		if len(p) > 1 && p[0] == 'v' && isDigitOnly(p[1:]) {
			version += strings.ReplaceAll(p[1:], ".", "")
			continue
		}

		// Zifferngruppe: "5.1", "0709", "4o"
		if isDigitish(p) {
			v := p
			// "8k", "128k" → k entfernen
			if strings.HasSuffix(v, "k") && len(v) > 1 && isDigitOnly(v[:len(v)-1]) {
				v = v[:len(v)-1]
			}
			version += strings.ReplaceAll(v, ".", "")
			continue
		}

		// Buchstabe+Ziffern: "k2.5", "m2.7" → als Versionsbestandteil
		if len(p) > 1 && p[0] >= 'a' && p[0] <= 'z' && len(p[1:]) > 0 && isDigitish(p[1:2]) {
			version += strings.ReplaceAll(p, ".", "")
			continue
		}

		// Weder Ziffer noch bekannt → beendet Versionsphase
		parsingVersion = false
		remaining = append(remaining, p)
	}

	// 4. Varianten übersetzen
	variants := []string{}
	// Subfamily zuerst
	if subfamily != "" {
		variants = append(variants, subfamily)
	}
	for _, p := range remaining {
		if abbr, ok := variantMap[p]; ok {
			if abbr != "" {
				variants = append(variants, abbr)
			}
		} else if isParamSize(p) {
			variants = append(variants, p)
		} else {
			cc := strings.ToLower(cutterCode(p))
			if cc != "" {
				variants = append(variants, cc)
			}
		}
	}

	// 5. Zusammenbauen
	sc := family + version
	if len(variants) > 0 {
		sc += "-" + strings.Join(variants, "")
	}

	// 6. Kollisionsauflösung
	if used != nil {
		base := sc
		i := 2
		for used[sc] {
			sc = fmt.Sprintf("%s-%d", base, i)
			i++
		}
		used[sc] = true
	}

	return sc
}

// **********************************************************************
// GenerateShortcodesBatch erzeugt Shortcodes für eine Liste von Modell-IDs
// Garantiert Eindeutigkeit über alle IDs

func GenerateShortcodesBatch(modelIDs []string) map[string]string {
	used := make(map[string]bool)
	result := make(map[string]string, len(modelIDs))

	// Sortiere nach Modell-ID für deterministische Reihenfolge
	sorted := make([]string, len(modelIDs))
	copy(sorted, modelIDs)
	sort.Strings(sorted)

	for _, id := range sorted {
		sc := GenerateShortcode(id, used)
		result[id] = sc
	}

	return result
}
