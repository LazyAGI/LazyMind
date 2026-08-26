# Generated from the source package's embedded LazyMind contract bundle.
# flake8: noqa: Q000,B014
from __future__ import annotations

import base64
import hashlib
import json
import re
import uuid
import zlib
from functools import lru_cache
from html.parser import HTMLParser
from pathlib import Path
from typing import Any

from lazymind.chat.engine.subagent.context import require_context


PACKAGE_RELEASE = "psd-2026-08-11-portable-lazymind-competitive-analysis-v1"
STAGE_SKILLS = {
    "direction": "shape-product-direction",
    "competitive": "analyze-competitors",
    "design": "product-design-full-cycle",
    "prd": "write-prd",
    "prototype": "build-product-prototype",
    "review": "review-product-artifact",
    "handoff": "prepare-development-handoff",
}
STAGE_ORDER = tuple(STAGE_SKILLS)
TEXT_STAGES = {"direction", "design", "prd", "review", "handoff"}
STAGE_ALIASES = {
    "direction": {"direction", "shape-product-direction", "产品方向", "方向梳理"},
    "competitive": {
        "competitive", "competitor", "analyze-competitors", "竞品", "竞品与生态位", "生态位",
    },
    "design": {"design", "product-design-full-cycle", "产品方案", "产品设计", "方案设计"},
    "prd": {"prd", "write-prd", "需求文档"},
    "prototype": {"prototype", "build-product-prototype", "交互原型", "原型"},
    "review": {"review", "review-product-artifact", "方案评审", "产品评审", "评审"},
    "handoff": {"handoff", "prepare-development-handoff", "研发交付", "开发交付"},
}
ROUTER_REFERENCES = (
    "references/stage-registry.md",
    "references/routing.md",
    "references/artifact-protocol.md",
    "references/workspace.md",
    "references/human-control-and-failures.md",
    "references/runtime-host-contract.md",
    "assets/artifact-template.json",
    "assets/workspace-template.json",
)

# LazyMind executes this source with a synthetic __file__ and does not guarantee a
# filesystem checkout for sibling resources. The build script replaces this block with
# a compressed, immutable mapping of canonical Skill contract resources.
# BEGIN EMBEDDED CONTRACT BUNDLE
RESOURCE_BUNDLE_B85 = (
    'c-ri}X>(lHl`Z&JGE`q|wIE2YS5+>DUqz^G*R!havfHxXix-'
    'lENRlZLB@jSikjyGq1OO61VoZ<(2x1}z0t5sKNRWguWMcTx|3rm*GxL-GLhrTLKIfjB2~e`CJEC7yM|dDIZ{B<E8TPpL+W-2-2mi'
    '<Wzxc)5w{O1jqc@tGnm%f8&)&}cDAU>1akKYEPg8eCTW?QmM|;!FTwCjBxvo1OwcpHj-{@-X#HT;XeDp@8ynJ!g7mu9BlN%SM&FE'
    'o&{OCBEx*xB-s!R?2dtZO$`4jo@;%Krmy&U)TUz|=v`Jwp9lzio{k5}{6-LvZMs(kqC-~3d+TE8egh$fe#Cu8!bT~oXl?XE@-#w#'
    'n+QQ<zWDL?-AzJbd0)<sF~zZK^X<HwVg)!pi|{`kc}G<a}vv~y7|%Lr?a58~ag<cXD$FL3{(iOTXut^Z-QxKTY{hz2*JhuarN+Z+'
    '-vmdk33eYjO|x_WlEI=o#iJ;#ga8%4(lc+l>;e=m%4suUePiw<ALiz6I!v%F6;-{Yb@DZ{DF?(_DQ@@A#H^QV*XIR7Xd^~KT4YX5'
    'Y!J}<-JmGSJspH3Fy@sarPS#(n1hqb<ucnq&1Ka6(g<G#)6=DjFChf!>;%HyK^di-doGJmhOT8c`A>S3`m{X!mBD?gS|M}_TZR~|'
    'hToj#04CU{SL+c#f5AHF!+mxpG4&25nRA01tvc6VlP<T9B2k$k-PWi<YXlNn7Es=K?@)oIxPc+Z`w%m<&gwzbK+{Iny}-qDlk?ap'
    'OB?&!*N_hj2|X4^X2bD7)Ou1|A4neNtqX>I@HtxQ)(_U7$ur);~{j;_|8)_=)jd%czE$=$fs-g+b3mdSSAxYa6u_Vjk;<oaA&E-Q'
    'EK=*)HLrQL63vhCTnJ3Xy8x-+svTR)Ls%Om+5c}C_&M|)4Mz31(Z-uS3pH~L5Ie~}3{pe!sm6uydz$I<$uxG;5bwji5FRsuVu@Kh'
    '!UUkwdjoG-=WvWxEGtKh9Om9Z}?&t=OF$OKpSm#h0vxln#fU7W1c`W{>yjb0p$#gB%ufOF;Q?w-x&gK=5sa4W}?`FQCrH-%dz8Ph'
    '~`HXE(WSvp8Re*PYob!Hzg^=d}OdvUzXtK-qpXkv!9s1^@pr^WKTla-63QssFu%J0el#$WYUwlI2~pTEe&PY&WI_bTOw^1t|~dOl'
    'TM9j`q+ixx_<>Ex$>I+@28EtCVCrOeku4>CXR=<dn9cQf1Blk3tCuq&5lGr#HRb)VSPKY#zXztF#Jro;Ea$M1FZw0@kGZTQP<d+W'
    'zJxh+0z+?)86ucx<c!MK_-Z@w80EqXEV=9@BR*{t#OL3o~IA>@Qt?j2N%JJqee%EE5Eg4L<b-Mc6s$hz=UC(n~*;QgcB^NXW$t#6'
    '<<IN|RvldT`if>gh{7ni20#d1`5BsWZ6uDW+uRvHIZw$kxnW%NJ<2{uIC36p2<uErzB)w`$B=tBH>CEB}(_0sp^htX6%ni}^*;$f'
    'F99Zzmzi`b~R6~hyc@svw)fa8%(S=spTMO1o*57+OB_>Kw#mBGz;<?!NcjKWUFV&Z3ZpF1;|23<EBjy=#0NZsk6;Gv#)y@ue?^jg'
    '8|18M>hoIJA07zSTll%8`U-58?9txWb4Ir|wox3#-l7pLbk>&a%42M3!-4tBKjI2w8-+uJV%j_uh}^(!o2JU<ZiFIEPQWh&$dL;3'
    'A&a4PKFR7yqnx|79dO=e(1^kcPHsw^%?yH7H|&0fd56kc7NZPu37;-zzWk?P@=D5_|5m3u)JUtWq^h*OF~Tqr^^%pUL+xQ;xM@@h'
    'Ex`hvmRe9)fl?7k&3xHdOYDQ)sv8D`^=lDE0+cX;ZHCtka9x@Av7uuujqtk}K##^pFw)=QQ094}79NcbM2FQS<te4%@MP!xG|bXU'
    '$=wKyd5B-7H_eY5EY@BHAeo8I|h)4T6Bb#`?1WUsg7n%c7ea_4qy`^}~s9k)A0sP~9L(xg(qyS2ONvv*tMdECV^D<VWQA9nS&_q5'
    '*5HQf?X)C2|4C9f>D&hNk1mHRl?m21C|>u$d4qVH$o$+h^aF&p*q0S}L?cV%+|vh1W_vac!A^1001$88;-H??OmtQKrLeP^l=Zpe'
    '7CgiY=ax0h?W&^X`NUHxk!)hjzAw&>-FTHjP{^_XL^ecmFPv?up*Z(CD0wpsEt`4P7dcd<QI%!^0EBEzs|r$g9{Iw<Oi=y2oWbW!'
    '9!_XbT3J_Pq}>S%AfqYo%-%T24ret8B(8JCa34Qh*1uX|E-WVN_t>u(b%>QF|vv>`&)riSWme!sT(5_|V>BN{x!!5F+7&+Nq$yE1'
    'UdTrp8bmnkacGFTa^hw&aO$8wp-j>^b*G&NBfm-m43;u9+;rD*+yypZe#ibrl#{<ikG9IZZ)`ISQz?T$o)Ta-!|NlHErRLcAD=(-'
    ')P7TYe@uuZVe<D$Bw)0uD=;~n%3p`$)-ZOcJmadvgvK&kYNz!dZ%c=t(dZZH~Msh*x<0A60g+@dg>60@@?OH-R$uPr@kv~^{Is>K'
    ';h+3u<s(l$Y(qiWxtRz(Oe%~sDIM#Cl192ZCPkTg3_)d1lt6^EkybNMdw3pr=MlyjDmspCp#KJ;fVONCy4h%Wj5`#6p(!_n}Ny$f'
    'Hyvak=`OKr3GrQ4YcBHBg#s|dnDva3y2x7cly2Q|AB*WG-juhQI-'
    'NU168>}aYOjc#2Wtw$TP{zxWL$|R|XRDEj|QN=?1<*I$5=Y`+-lmDR{#ZPigUAa$MyL-'
    'Cs)Xz)9x8H{r_RB7Nc5}}w7ZDDDZE#qO&hFV+{IE-mCb420WS9RK_oan%aq>7C%*#G+bPfC|rJ8p)&zc-+l9}x3xY5y8|Cq}zzyn'
    '2Rg>DrOna*d^e^mH}B2CV++F>}nu!Rc6>Sn*ZRPa2p*hOhCZut>KlVZHQ)Pwg#yewze_S1-?43@*fb7X#(%1m#Vt0WY?I^oX1=T1'
    'A;zeM;2cD<aj%H$r!k2f2lp_i5IezmM#RQETqIO_k=gfKrZ|ETu-aqa13ZSD(NARL4n!L8og*>+A-N1Ld=n@t~QTie7V>aHKsYp?'
    'u%vgO}>J0s?wm}K!{U$j@?4&u?k72>g)k)i%V${JQ%Bs83kc0{j)XM>X)9mzRgz!?;ab)LS&qHR6e->T0($z5x`DJr~QtfLH8Mhv'
    'W}32rRA+T7|Tu^i{3!l-!nT*u&RD2G{Zyv2*#^6%R0Zf)(cT5A(fLDR=k;W0*1dKND&+mCImqNJYnCtlL|<M?qg6;W89cg}lWM(A'
    'Xx%2j$vqS$GCQ!mJ5v~-KNdZQ<I^P0XU-mwJ-H+QYOqqplu_(7L=wH@vHfvnxftv9in`e(LH#2<}|?iMxHY|?Jr%C_Pc19&?bySV'
    'jo3<5_6>oPwpb{U7o`L}_`FIO?6YBB^%2~nxL!omQJcA*K@g_UTzR9Wq-?w-YuPPyYnvBo=Vehw*clJO1ZMO9Y6I)vwPx=VlH>%P'
    'VKULjgIqZ0bMZ%NjyvNN6R(;vK@q4`C(jJMVudbAO*oQQ_MC~x2>IpJrUQ1<tmZ#G6v)Gy$Y^8>TV#W$BD5bVeLohY+G1;&_>C|4'
    'giF(>=spiI8(F}bg9tsh_nb#j;<B8?Z(N2iNkFxlo7t*`HCa&`PzJj-vV1muZ-sqY+wfb?;EGLJxI3kM_`FTu}J2_rT;o$q*Zt2z'
    'PCOT0hXEzlSu84!zFha<N>%=eOQCZokGi&aItSl&oaRy6pkHoa@&D{n5W7__n|u*Jl%n%%W$sm$T=bQ_XOWtR3Nrvs1CDS+p5zEq'
    'i+3ztsi<30|lY(H`Ej}K_xR#(NI9ONCK2Ts3~1rzBWA04USUxo|1G916!;t%1dPmENyMCpu(g-`vP>_C}SzLQyU;=G;|>J(}cA+f'
    'J*mT<pBDh38nW5C0Sqab7fbrf9fH(d36PNWl~c_}GIfh9`{V=pbRuzyb;#gh-<wl3lD*XE||WIQ}`;w(^^+|{tzN4KiahAIn>=+b'
    '-ZSS%#}rv(&Ys*XhyucFg67}R>iogg1a*mP$kIvWhvkg5=;AU_;Wtl=NU6B#`gO61kjvL6{vT_^+OVQPb$U`WM|Flt#BJp+FF-+Z'
    '%rTCNQjIHe2_oDYg*f)JPaRwEDNENs^n`{dt@V$1fCfx*0f(I<XDr94CrowwvpmK|3+ouX{vUKH12aVvBuc!^4jX_;DoXl!3<L?j'
    '4iY^4X<X!X~IC*ntYjI}e8Od%}ZO0;{NoXKDa@ybzje%I+p1}yRiHf8V`7S2<%$oacCdMHjqbsr<LUom*fxP2JWb~d}S%NdSs5_h'
    '$&>BgNKZMha(8Jsv>i)I`jS=W{6$`=PNaJe9(5}6^VCtiITo-$b(s%}yJqC&!T!?$uI<(*}>j^B+I_RSBHPlo2iVBpL15$@$R`ee'
    'IFqnhR{EIbhvM7c*9g{`6!$u}4IB)eCx=N&2wM{#i)=Uu$P!`<j)0M9OOz)}f*g$*%27)?E=9N=$ZSk0#XlvTJWKlR}+*ps6NK2R'
    ';sstuH*Epf*CVaYIVcC-_%-@7PHUX=STj`J6%6P43lDqf5JiXGQCSULGZPGxPnFPh%XT+jCOh;fqbx*0}_5nGy=fdE=qsJ>Vg5y9'
    'ceVUrt#GV$FEEak;7P0EYw+7K~R<9u<80yoogv$ac%A-HMRyIOM}t8r7eQ@T5IH|+D9G>&qgwdOt-Hwv*J)yZ<7+-JGAj?UY;_MW'
    'C&V&Has{IPta9<9754)9<;e%co=Z@5!ajHZy8sg#qrjRp8iLmV(uCyd9@fxz3XK1K2+|A+>wp1svAv8T*<^1mD(vCN9PB~GMKZjF'
    'iNS(mx1iT$ZRGYO-rP&`Z-kr+W2rz;$$TtKrqetB+Tk8c>@GQ1i&PsJrM)v5Y0YJC<zkMs8-55`AAVSwzA4%!w_O;|s47-I7US#`'
    't_RIkxBkTb85y6orKR+t`5-WqA@&ff0C2^TRR^)HGcXo@xrHN?{g;tTMN<mNcf<;{5HoKM#X`K$%xi#ze&G-A$cvz3<*)MvH027f'
    'MlUS5IWjEmA(wEINfE=g*w9*bJw=S&lNjl;J#lU=s88ta*s_Kx;klbYULw{tfUKY%tXTcn02tjOGGT|A;S7^w?Kveq_vFUlI6cBk'
    'XNh{d209hf9nsCzve;&iBkm(pv+<ES(p#)2$twRX>LI(TqVeiUZgWRuw<Y1G-mYtuzB@1w8Aqm9{A)&bgLd_=6DXb7=0_u&E#Nq!'
    'FRRXk>dPYhKV8Dd;P0EFYqwA{GW+ICZxzWK)*F8sS@mrrh?uJ`(n!EHDrj1x{_HoOPVnWeLw`Bi7G{k`{P`G3-JGxrA^ccyTo$$@'
    'BQ2y>!hx>8<3oH4Z18dZ|$_S$NhDhYdee9$b&`Fp|OuT)h*2#KmzoEMo`nS4C@1@hIpt|j@$`|sg+&JHrr@Vocb>4zG|{@oTMwf}'
    '0p6O1w#mIk-~CFcX)9bMhcaP?>gg)d7h9x~oJ!B7j-OXY{v-I5-regsu#H{!)*jXBOE)6zEwCH7$4w9`~kB%637@=CNxs9t@_5hr'
    'hPo04681+ff}yEqpeuv}aHs<yPo({^#PU41&pgD-|K2T85%;x$wE6C1<jL~%SS`<v15{mjoP(=)%$-uN`v6Ijo|8A-cIW;gNB$Ik'
    '|$p|228V?cVb6wehe4-~lHQI|uS;JxikrveOmD|!?zfDuF(d>qe=KN(#ZVL-s?%hhO5R_q7@J+Npr^`%NGNh8VQLRIQggZz_Y$h`'
    'kkL;~!V6rs=*CkV5S97kvU?pWyQ!qUy```B2ue>7VEg5uIvNtYS{tkQCHa@QLl)wSpG*aI9loiFtXMPwHY{(zYLmYRszAb#Z=!ZA'
    ')R{?*$VX1v4Lp$?P#|2xJbGxd2sKE&^ww2<z{F48sg89ocN%iAq&#HF-9MpF~#f9(nKDK3T-I=gO{s5gO~S15`i=9WmdOLg;UxMO'
    'Hh(}C4I@00k^X|(sEQr=D?v~njgB&n9o1rojOJTSc8CHcI<wwk;fmbhq4mqV_qEc7zk)Q8I>azaF8hel-bgn#(pSO3`jKi~W1FXS'
    '~EBSe&bau*6{cqTg|7v)1ACFAl$OSAFxnY@$*;4E>}AMOojJXr7%QBoRO>LXe*(%G&TN1|u8M7r3CVybKKK-^o|j!yP6nR&LUw#Q'
    's5@6Wuz{l~J1NMKuJ8-aSCyn{`8vn%^?Pcto3q$iuRH*VxQdvZ6M+jF^_-A%onH?uur2&qk66!%OPUhKx|`JSe@ux%(OnEnY@of4'
    'ZdR!tNo5#mHuihM2&;f7~t9Bj33T=5`fs_9@Sm^GzuE><RNU^U9bypy@aC*r5-T*@b~M%26w%|v?g@<8lFrWP%X3+0G~Elt`cRb&'
    '=l2qqW3Q632jM;8L7&O-zyKgGZc?}iYqen_E|#%(zn^4D)?V)36I?S;^!<Adttp>6@o%G1<S1Ki~We3nxa#NRHYZ;uKUi(f+@YZh'
    'r;?YoO?sD_(Tc@c-1a>;-Jxto1L(_(23J6gjl@$9BoI@Clab;XMxf0XEXk*lXKqK6Zzu*ST?f;K#obzDU7LWeDDYV%efq|pG8myg'
    '~_#fTYQ*PvaZG#R|`HVty8nsmXE>Q+v~Ro%9AU+5<DsSh9T@nIhMnapx3I3313e83Oegdr(i&bkv)|E^eV5nM>!A_U*l+uf3BaAB'
    'CKGt*x&Wi148$r$Jz$oA)8XghYa%kO$~=8krM-r95H7VpxM$##ln`YhX)5hoS;NP|@Ii0<vtQ4=xK$2tNTRl6TR3oLM*C4a7H6Qt'
    'ZpASyJA6qRNwrE}A7M-vxEYdCEhNlw{B%y%O!AAHB>Bv&wWeQ~}E?RKe&WEo{WtZ|_rvdkaT02tyfRi~d>6(sDqZyJR+T!L5kYKt'
    '!$abA}y7R-03;8TgxFddb^WoRL#qFS^yXp_;JVt$d)N^UE=r=@cmKseFDlUiS&X44T7y$qX~;9tfWZH|VAxLO9#I$J^x0Q2*-Hav'
    'Q9G#%}Y!qEuA;Qml;*Ljnas?aZu@#9c<yCbdQnZZ~U;h4wt78s=@A&80|TBUriF|X!*pTuNZEQ<PcHwsMW5IN=TVj4Y`6VQ|Gh8?'
    'Uc1}vJJHn$8_{ctnzxO&91SPEBDaGEJVa1B$kImR0$hKgIzAim9fStH(3;}T*+EYpp}>Mw4Ev2Mb^61z?gMryh#WrrR_gM%(yI4W'
    'lqqApO8j7RZFx?SFQ>y2!8cdjQea(i;OJKMyTc>ABaJKEp)(HsBzQF|uy(Hq@2Zsl%guYH#5>K0-3(Hj6`zx(#PAHDGwzyGYYyY+'
    'f&TWil9ey20n)Y`6j#5RAKd(5@gn|7UhZtf2n%V~P&op&3r?zz*M<Mnj`J%6eAt8I1(T*|Ytl)3LmnI8T-{tF6fdjxE)<QvVJUek'
    'C3zwf@4?aVcKC*BRn|H5l?|C0U_o!ySEY}cLCdv^DB%D{3rb2@_d-nKUT;%4qfD+a8ee~8aAnSbT~g<tyz{q$!)32w}bdOA8=Z-g'
    '_$gO$2Qlt-`4p3jw7JT<&5oF#ex&JG!K`U0ISUT~wgs|$NTG!9VExZvOLf4}$d)|qQZ%DI)V{h|A!Lq5Ou&%Gj%uYPm)wSV*fv*E'
    'qnCH$VydWI^!Cq=&~^{eY`9XCD|1&LqvbjhSN4*Z{cv)Jd?+B<r3;qX3sgE=zTw+fYJ@D!F6#lIK^3+G~hxMfhw=J<f29vaEam5Q'
    's4<M~GT)K;7h#Gln#K<@r#YkRKSRzt?pgp=HztjI@i<l0(4X%%PTT6^vfJ%j_K#~VN!{Ul&v5SFs1f7JeOp_oYT<p1~N#GikLl3('
    '2JxM^F^U+J$xy$QcVEam<wQ|%s=@2`{H{ZDeNuXV_az~{nm{?$!TZ+EWi+9w@ZlLx`n^<=v~$@N^r1>v67jn&(&>vc^_1OCv{@Z0'
    'Vi9TalGkJ@FW@u2#f$vwJzy0S8D_6@%Hw@ZbF+dpDu=DJ$bw@M^z`cqrIYgZmoXK|)KwG)3$Pkb<6Dmw{b3_jOs&ffSG*axeyFv;'
    '>A5xJ>z42ajYP68r%TiIwb0XI@t<^GVnp(ws!(<DID)TKAFomt(uHW&Ua@MTxd)C7*D6CH1bS7?;=T5J1f9iQrRK9<Q#Uvaay3$L'
    'j@!xfj<y5w$NbL)2bb#8gCb!L0JueeuNuBYpcjJLb@_7zuTdwQ}rZr#=uwb#;%+%^C9R=AM5@`0ATx~40oFdSHk^W%T-b>O9!wRL'
    '1~=598Qni?v1?K+~k?It){iA>WYXVToCqr^w!wFYhpPh|>CRW~&Lz#*%y_>w5AhU1<ft{Q%q>`FO+J+~UZ51?3ea>2go?dtC6YWS'
    'IK7g>O7btYJP#4aXrjhVH&;~GvCHO{AX20(+wR=1E&3dzIMGEG%n`z`shCy{93Qb7{Q(m;%~cegslo_@j4vaM};hO#&6Z3i8G;5D'
    'a4{c&r1Yxga;Jt$0_;(Kj+<nnUHORj7zWK{HlXn(r0aLUe4<5JNlQZ6e{NtGTFl3~?>u7ZVsBE7aUjj(h6K5$LTQ}7tYtI;YyOVo'
    '%FR6LH0xD1H*yQj^_(UcFHFHRrUw#S>9v0*VH?|e}jLAd`#-zA6vKh(v&XyHsB{kSr-Q8`-|pAL_3xqaRipBxQDi>1mJrOMnX#x|'
    'Ib`sc&f0T?z&J@Hv$L?I<bXt^RM7|8m%?=fFC8a$L2NP#5!CgYXEuloRW<7J3qIe0`ovkxzez#xZgzy%6uzzuc4@^kuTby=@eBCE'
    '8HD3`j9;h0}roA}%Je%bs1{`;$6zLhZ@RbQ~!oo&l?2k43Jw=y66>{qIzGRY>(bi%;XLd0yQJ=@jQ@p<NV@5`q>Fs?Ff9UY(cc4j'
    '^o&6vy3P|37*XR>V&?sw!TCb6xcG2_gLbPeNGn^Qw*s6u;!SjGAygyJ;=2q;s2%5u?#qOV6u=5FX!W#>_n4H8GXoUaGPl;xa^rBI'
    'fyU2Ip0WuDC9GQviKucGt2*!EN72n?LvtCaUO9X9GKZV40efE$Ch>FpDEr99g28$S!R;c+B39&XD&TuI)y!QAlQKYjmyHY1{@nNU'
    'qMCWsp*EEpdNw<)%xf~72!3Y?CD((HhRxi~HJiDx&Fs?+DkOLxWXsIG1yEhCR)uDx$OS{aI0Wj%4oOhhqozBr+nhe^L?8mAk#yDg'
    '50jG+`sB8iIA!NsNNYH<U}#i=iYAeDyc2n3LIjz>1FNXr4iv<{4gF8{GAEv(C><un<G!!XUL*2ksd8@M12)gv)kiDWpRprGc@Et-'
    '+hh4nF%Y`%sDvX1qf8h1ol6pQhKaxVmV6hOg^;Obuz90dkf-'
    'Fzyi6xbXKU}`EwOPC0BFqDJ-B*75@!tLRD9?Eff?h{e!#zknkEYLa-ro^HbGZ3Dfgwc)Y{wSvR$|RXin*QY5CI^!kZavKQ${L)@t'
    '8c_w&Y?71OB`~a(R^zFNMCUgicCzs74BCiH8EFsBtla82LNMecA4!-<bPf+if|&1x}kLDssfkGF3{qZ!nnLWB(;{h5eUPIQqAS8i'
    '(qmO5f2>!4i?2Xb@jG2)_YujiPq>Gj76j8v;e%yhkda8MeQ*l6j5=U7`_xdgV7nJ7&k>ue;J+hB?acJbgrHqS7ye88Zp}32*Z7rY'
    'tLS?4wqf<npzaqVwK%K*@0GzLVPTf<*9uI0nNj0fbsfL6fHVDp!CDbt6X+fs{KTT5}||{NiiY>z-m?kga)CZ^i*4E-aG^Xhz5L7#'
    '<YtCHeS*^?;6a+&3o|{qUSiLr!SFPl@aZXa6*%GTV;8}LD%}Dvq3_LqW<CP?inKPERmc&NXk%bFHcQG6Ej5K!}7d49?uo680mC~X'
    '#kQua9uCXSL1$H0;%V~7dcSh)WQAvS{$qg7#o;rVMsQzic@Gj=CLhhc{T1ou(vF2l;|9SctuoPRtiG6ut(klGTqkt$*msR8VmcC@'
    '{B!DMSh*4OiT>f7_yRk_iMw2=%fV6J^0F*7>ZZdM5*jV7)gndMOe9EwMFH%-tN8K^s$UOz-n8$8FjM9Y^q2FY+Vzo90CeyO6QGsJ'
    'i#c*oQ8?kH{S$OjjyX2dYE%eU}=54Vh^00@eft&oX!Im460P6ghz$_JByW3zdz}-%igMXB0}rbJg^Lg&A}_RUM7f%#1C|H%lspwR'
    'k$C|JcSWCF)XGIA!QIU<v~zhaE@m70C`_5#cO-KEM0-2)k6S7YbAa75~?X2a|VyHSWn1Uia{V7y>KK8dvS6Ch`34_{;aMn!!7%KZ'
    'D|b{68(vJcM9C$C%nG{j?Jci36D@`K;8n>3zlwYGA*oq3)Wew8SqPA1btB|$-jNsqL`yvI`}9WA51c=v`+nIL!t7xLB*ZiQnOW75'
    'eqD5SM68W!t(<trU?$T$Of3?i(AHqqXXu9ra&_$11V|<TT~Dc(8danVsel|8YmS7qtkYD+ik!D(!TCn6fq|^1oA|oX!t}=V1ip9m'
    'NuR~!0)Xb5j)Ogo@@ciNrdxf@^Rm0?fGMzTpeAtf4XvdUnIQ9PY*DGjWfJc-QB5_@51CfAEIM}p=kw~v*{koZ#_{_ZS^ZVA>~8+_'
    't9=N`UNrAdUOG}h-WvV^I2#aZqk`?*-Q!;fw+xNru7KPeI8@+#}=ynj}^s-Z>#%ja!^@!Dg%&XOzdW5{fsKaA8(k6q6Ne(nJtUC5'
    'EVwKAiSGVg59*V%Df6Eh}N~i+kW>O*k;wufvC9bL81JPAUW84I?qggP>8Z<ECG#(eAB6=gUQDvDh!MA)tFPh!FSLmn)~+X!6bK#j'
    'ir<%Npe;8h}(p7WmwQ_z}Q@rA6NH}<=;T$&920+W@x@)AFPO)+;hTGE$fp<wZ0X<7_x-%%88eJkgpp3xG102)|OIWMdJnd@=LgTD'
    'ob&I)Z4XVpivuxa@SSEg_v`%lX>VB3AJyS_Or3**+6KAP)sK{BS+JqMrtvh+?0I|8)9Kd28RzP3RXy1DAtx%>|3=k{OfvvJv{7@F'
    '+dW6DrkdgXz7acH`GhI)QW;lJksZtn;jUUy49C=tEV}0=p)fVGRt(32Vcp#^t;P#7={>zplxewLvjqUK2xXB)Fe4_qT^Sj?!pWdr'
    'wxd@><ne@O$ZRi+{9to1pG;c3XmHQ7(vjAikGi^xt-R^IfspJsDX*+7sn&M=D=*`{zZZ&34w`T45V;r$#yA?`|aY*1|PoC?J}6JQ'
    '&|d**ts~~Ypx9sC(dpDfzDnMzoUe(ZM6svAR<UoMRh=itwRJX0eVtB-G}S#{!Qta9OQx)2aEByIAUQUy`jgGoWwipC`-{fPN1ni$'
    'YFcP1Ig9Qq9pg=j=A_~@ZuCi7TBB5AA@!ZLI!(-o5q2|^QZ%+=k2SmLY_7E%z$}qY$G0DcSTSAjAAB#2H_hBTf)wkRxiqktH4<h7'
    'k~Jb7=bMKVt|VJQ7N?&tURaQuUlnyK~$nYtvq5NCjhNTp2slUbn4?x+C-D51nbmS9p0CVxe-}}>kXu60UTZQ-1uJ6-V5U2C@7<e6'
    '*(`-F8mwWg@)KntTyLL%YM+m`MQM5V2(BDhhw+AoRC6^Y%s5|pYob4St12$3>nxI;!xIo66lV8?>#po>a}uI-F${P(%hEkH*q#4%'
    'W@2OT+$*!1vMmKyfjuvaY+HZ&Xq2}W_@i5A{0*=^=^i4c|v?&<Ibaa@1Gad!nl&WqgXILF6lkUMnqJ~o0c{C;oBJk3cSBfi`?XZ)'
    'yjVPOm6>O!wULN>K1DF#n>wh%0`y9mX_<;?pq(Vci!o_)zO||0t69~_M5q`Yb>yPyYo)Q#flV5khv_1^!BZu+ieXu`YhYl3f%K|x'
    's?o#F>4Jtsd%zVN&1l*ZSpCUQ1xD#?(m9T3=g2jXAi_sr;8OHXK&r{RCxHmtQ<EZ7F&mr`T1`FAsaVUH#8_JEc25OK2QR&5B~dqq'
    'naSvjpk71CqMm1*u|?bI<O};flU{wSAz*!tQN~o&vLA~cIHRT9`J+{mZ)&=y4M&WK-QxN>FtSVH_{N5o}to^dC`Q+qFDd1h{})Qp'
    'SYNiXhSU&RN1fAyG8c$a@JR<H<IoAx2}kTw25*M%{u!QVMkAzlxdN@lD(GgzSg0Ip!D{A(k$^#bx;Xh-er607VdJmyrzKa5>|-XY'
    'RS<z6dMJ(FA6eCmL%a9*v$@Bxc1yt@m!LMN*ppDUGQvBQDH)B87KLo(a8*J@sf&D*55E<k9Ag&<or(pse+<6s|r`c9Pzq2dFM@E8'
    '}92NX|Za==h2>X;wSy=GTKJo8g3~Fd4;O8p4ykI6TF=&7&tyK)s$b5e*jG-vPrLZxX`Pxv6S+(1>^FnoCQ*QP_c-Vuk6n9H%a!%W'
    'Cra={SLdlK~BKQ)C16Hj;Wcr?(8)tmg<9sypybb;JM?-Fc5k)7;nsL!Kfv(7+`Xg95j|gZ_H>6^Fv-(kyMaJ%|wKzpgln>(fxtZD'
    '}J#L@Zw#3y|#*mixY82R=8Dh=r)I{n<JE_xVSVUwyP65FhKN{;_-z0mZG3ew{ye#lNoAw%n$d!YC^rGSjCW?8HOEY3n3=7vI@6Rg'
    'ra)Ml#$JpkTOu2LRN3<4aA_Kc80~Yz6|3_cahbi`s}a1ny0zDV9qa4;#M52y2T$RbM6=WVzcDG31jdLdiaeWy@6z!8ULXS32Cx|G'
    '9(9`7ZXEcE~l3ZP*sy!AP}T@=&Gx!bg9sl7t*Q6dNbt~RERkqEg9vrazOg3@mX$+g!(l;dKoW26r0UtuSQnY(8$pGvE%qhD>%yhF'
    '+=w;^GErxsi{f-i|<>~ki-_b(q&BLHQs&uot6x*QwTA>=J{sHo_(i@`7Cfi*`Ah6b4r!SFW&#)R~brY4GutQX57O&I`yV?$NQ>qJ'
    '$w<RzOrPlIyQDM!v!Hz*?aqTw(E|*i^>{bcI#zLQr1O}UXPdE2y2(^YVEe!Naw>@A$D>4&`XSFbEBd2Aiv#kZgh0t>F&wh#!VO*^'
    'YxAtbLs^4J7r#PBjGDuh-Ry+RrkqmE@rrbd;s~mA^KA7ylzfp;yBfrR+Jbj6X4QNCb%2YIKKuQZR-uvIP2RNij7Je#<l}0q-M_L^'
    'K_e-<<{@xS*QjC!|^1LBj>9!nf5b-4D0Z{@1_PuD%q#P4?od^Q5OSB7ENvITzijV5nDioW=Z<~J<XfwQE34J>qnW6&)cmwp7Z7}s'
    '4YFgml@|v46wxCPPo;E6Wp$>7_IHWAKkszyXEn{J){dGF0ARFdpoQ?DSPvuvNvReEq2W1Mz?eA-JElA3u~*#W^5;TXX7Wt>ZvO8E'
    ';@IkmO;sB>e5%{_}=-t@8P@eyu)V#3f7{TmI7Z-oY$!Dgu$O=<$yecSW78!O()Y&a+#L#TK{=8IAJ!{=qQt?6zP?hX9!Me2rvF>E'
    'Fr0}RSQBf{*b7#;ZB8__$_VO>$x_a!nTgj_5W|Resaqg*SdPIceUPV@nICmD|=&OI|$-hNw6hMkIWua7W>5_$9V!+xP__kxn#Cn8'
    '7t~L5tLdu14ceSlyVhh;&WYYuW^1&UJuwhCLvv!BboHNU@l#UsXWNjR60hbLX@BN+`{qz@tCu1-PZumXlrfHB`Zl`QTLcYf}kuh^'
    'rLJYE(u2XU6(NFk_52D7gA)qa#?QoL`<|eidvGtXWMd)GVf+9#R&iG1&IBcy@>x&{l4>zy1m^kKT1U$`deI4uhG!_lOj*;NOk2<7'
    'ojX6ya%HU*Lu6HkdH&meJls$*0r9l)=xgkb+vfA8&+;6c>s6|3|to^+`RA$V-+v~C>j`S5P3<M(%B$OL~3*QP_L*SPB8zDWR!w5$'
    'QTpchk7gJ-)P|IVSTlQ2!pa}2xTqm_PWzY0h!(9c$u?c^n*MljYu3HwY9-aO^U@9&VdGN#~tm+WsE_YXm7}eRrrq1MMarq?w@<*w'
    'RE*Od-2^trHB|4yGu4eZ>KBgXlwCFl!TRw^|#*SO?)c4s^Em#lxvUzB5~;65PiLH+ndY?x6XK~k~efy?b=j1dBR3QKTNAv=r0C-)'
    't_O&UWJWOBbFI<@0Sw#;-qR_?)9QPnF?f!<SN`JpCd2zMxtFt0T*OPjUh&1f<|F=JO;_sKkw@)`8-pcWOsTkq%X{>HZzGDW>yt}i'
    '<~MWm@c1cv|V97v|8NY_^{_Sm|U6sLd-`el>=MSk-6Yr#&@r|eTOZw?584!b-C}I$8$w^lndup59rFO86fub1~@8X=LW7+x5?*XH'
    '~`ckE^VN;v;>dPs4C6L^ya<FWi|yd|JkytX@gisR9Vr0o8~utp2#q62@Q(4%64}DWBJm$1ZkK)bzXFsH1)q=(o`+<`<2~QQc7D&w'
    'Qz+0y+7)`WzDEY$FHKoUYJ*ft-m<oXh-eFbO1oPb~fgG<1hj1*nj4u?FV8I`FyOC@j4x~XOF;)qk&_W{W`Gw7v>a1J5J+3b{!6yN'
    'JV{fxA+U3!Ec^Ex+^jWogSFGg+sZ<Cx`G{g&q<+$^)t9E*minh?#`6pzaUjYcEVi6T?yA%V^<=a;hca3z5Uo-YGCM3s0i_o~;bvA'
    'ASBN3_*nuQyc*X|B`%!Nf$lTo;BCZfk0V1mFk&Wn87yY^+(6v<e<gu6mZ9NF_k`}pXlzOnHXO=O((%!9}w?~H6T7}>&LBT06<4{b'
    'cwcZYHc?OYPLXc`={+R?2StZ;SxW=&W$tTBM3XB-;LMB@<QwLaF%Q+h+uGX%!ARa1os}(jxgf#+Zy)Kbg0Ipcvms?f~-C^kSYjXu'
    '7Eua=_p+jCUCqIEiZeLia<~}Hz)yGklzBN$TS+ypH~JqjRmxPY8L#^9Gs@mA*bo+mzs!?H1uFV*~r$S(Je$INO_^TqJ+T5rR<}P+'
    '1fhXJRU6%S*o6kGque9fNdQf1HChGA!c7Oia#<zz=wNQ2to)>8x~O=BWuB;w?kRN&WOuyrbUs-9>sg1&|QCQFctl=_){0BlPOR*C'
    'T^!}4Yd|W;u#s$7h=Av$}l)bDl}(7^m-H1;|av5qV~%C`Zqt#fCfw7k`pO2CpJ}Lcj>f|&(5`!nS@ybF=!Fe(3Vd{FtyPPP#lhGN'
    'd<&S5ao%WI0z`|$TAe|9=qr{5dyNaMn`R)T%Q|b&Qk9awH)*JXe3%*{&CR?R~DCL`0j}7a4d_Yhyt_Q9-bM6{9SZf;IT=ix*7Da{'
    'IMsuQH{(PAr2HHh~<YO)5G*PlfgbmYV)YiV=~x{Zq}l*>S@9AYfdDHVU!d{wu6En?0|k_%}@|U2wWnD&{3wSmtZj%jlWZ>8k(1>Z'
    'Bt-68_=w_S-7*T<j6CUMTLplv%D9x{#X&fB<hVi4A-mDq7s7dO9D~7M$b^5MA8m=vFKDR#>zvQ+F|r_nGk$T;)Hnlg?K#tf^S4w8'
    'jlY%oE5@PbP*Vr8eyRegBX@TQ_w8A4owmqJ)<a6Gdz`QgY=@SqSfRe>ZDLPnU>=vqptRejq-$!wnI`5#8Zov{k6*79Y*kIcyKK_h'
    '1daNe^PzT>=`m`af{>k*n`AP89QO!4&*5F1)2tQ6Q(6*;MUwbtIwlS;v0XnSltiBhhGSlB?fr-3XR{=-+~B8bmYQ{21xO_JKH!wb'
    'b|a>)OS4g<ol;VL4>idUojS6l4<LZLaN@{oS5O}aNu0+oY6u2a~+$@z~+q1hiG;(GV)!O&o@D~*A>4Mwxg*d%r25L{gur!6I#||&'
    'E<V%nWFqGnW7Ze@ep4AFMslDuvz}%eG4;dw;F=9T^}`+S|sTUl9{o)+aMae;RGpnfvvZTb!xB}!}6m+7<6&lf5@;`Dg^Js@E^X{-'
    'gB!9TokFSyt@3q|K6>hp3d$cH8+3K+H<S-`rC4a%(z|O-3(dPmb>{$t_fBCO`UDMpR~4jH(zh-xZZp_+uGjj8}Bu14NrG-!y|mD='
    'J%6WyieDO4qDyvm5#(BHxs@?kc~_fkYV{fREvym;TBUHAT?H<0(n_aaGa6Fh+aIQ<HsoQhyQyQ@Zy=Dilvg81F#>oT{;KX^%pR0+'
    '>0gee94>YjZ$ywaH^VhQ`5u8tK<Bur1He#AeNfxPk}Z;HLUR!_{Hq1_s2}jb&RSdBil?73E{i()hnRjJkPkrzti2?lMAX9n{Kvt-'
    'vGVBohw#ff6&^|OooVWdj0T*sTrvA*srX*^RTTt__9`@Bvp5EM|+p%pZy`50r92C6;`o{=GF5>Sb30+O5Q(6Yc$=?cK(N6z3yJ=x'
    'ArM#)g6lFo^MQ;gt#?wNYZd%umk&t0;4o-sT?wv@64(Ov0^|T1!`Sp7wx5g=}x5zweE27$;=>MT{;7Z^4~#ud#?L0-%Sqpx7Y*zz'
    'Ufl{f@~#%bb1*Yh|YzJC|8T~&@}h+K(tdVoRmbgLP}_+M(*>$&wiDm`ZwIogYjD56tB+UZEqr@!Wuf8gP-PdU4M@bQ=shVZBz0$m'
    'oB~h$lno<4;rNTru?)CO{Xqd`rhkptv8x4xo7IVQzsXQ-~^rHOFUTaiG0196}G|~U}YzWfEQpE*EbcXy;Uu)QQg<Jx8y6#5CczY7'
    '_;EWU@V%;BK+{1@NrOeB4Zc#cvoZhSDB^Vrt);}DZ5?Sfg;yc&xa8zm{4N=O-&N#W@*9kM<Lvsp<yDYsLM3+xWT6rhr-w#lw(pBY'
    'Y|zGmazB}0+7W<rews)hll=LYvS_yI@%Ks&u5gco9GOY3YF5+PBQ7nxUObrJ-Tc@1iM+~wdU!y&51#DD&?W-=s<ilC9fyeaa34AX'
    'jGq4hw60rm;kNhaaNv)EiOzsfYJ}-+5k2KtAe#{ihOoaxCCtH@g`!Or{KwT*px;Xo{Oi2@{evLp|6S>xhfSLK&t-c?Tk0y(l{|6#'
    't7b^Ay&)7wYbI9uGnsv$+gAm$V(ETR?p5@%FFs`?}vIm0`u2+r81I78>i7yK?cW^UWQ#U0Z3BZ4!s;^(!>Ttx5Jeai>c~9Qd&%;H'
    've>z$AM7S^G_!uK85G-VM#Fy$jx=oL8#3wjp4h+uRyG5l%qk$(>I?xooqHRw!+=zBbUYno2){5LX5U^*u!D~x=_G|KL;I#=d(Cn6'
    'QKaZT1V$YB}&IggqIG-0Nx6JGJksD*y#P-pPnxkCz!si%Ewe)2?7>{6<l4Ku1(KDnrhg(aw3k_VKb)SY>bs$=5EYSRc8w>H^Jdyp'
    '2iHV?h2S>cZ&B%JiTe&huf9Si4brf?cTj8f9b=mKCqEW|9ar%#mP4F!;p>B$Cy7c_R9C7iPg%;@THD$;9#0x7kaoJcNTcKrZCilm'
    'QO0>`!J4Nep+@HB{9M%3bj*L_e_~HQl}NPD7wbp`K8;-_zU7p4#6rTB$70q+^(KI35&g|PbNggd;ix56}Z1d>LJ#`mQuroO6esI-'
    'slRl`{fg4eVk|QfrGA&!tz0i;%AV<D%;Ge8Zhy~bW&;B*Q|m+Ma|E~nqaE|5$`zv9Ma87a}MSG<US%RsGuzXRoF&7ff`(xbroW=k'
    'SQ>O20lDsE-Ii)sy7B3>jq%TXpn<-U5ffd?Qfc}VfS3@3Aq|)Os}&D9F<FPN6xd4m&2tJQRDt3Ber2I<YCVr6S*WOK6PBVhk$U-I'
    'F!VX6`A#S*14`N_g}$=_sK_Z7)~PF)tYT0k|H@%@^o|(Vd7Y`#%R?dhO4m%5FYCg(v%T4;w{<S_v5no$j13eW(MOVFRi6$Y>g$Ag'
    'eUjrO`z_~UH~W}>v*V*dlwRi%EIt;I^wzATHidUCjI^`Kfamk$+ot2|6NPQf^mbdYO?^QwEWoMU-A`DiKXX|9;mJwVeEsX*NO*Yo'
    'USxgSuqHt)nZRgTIGT0q5u);QR5*dl5r6%p^7`4D6<0k{SqP@!g<sAX)t)M_sTy!XQmSA1D|vSfzlOMH;^b<Q@g{LzBfQBrJaAIk'
    '!WL9ES-x9hqqA6#n-k$dJmVG2E;FjUlcc{^nm@p4LxPu$jXjF!m+BmRteF`Ky-9u%4>Jk(FPc06HkjRj~+fE+2twh=z+~bW}(Fq2'
    '7Zy=*BWc`F}Yg8kV>ehBh%WhoX@@OprC8}EO*n>_^8?VR;DZ0*#^=A`&ec!lWYG(T&rA{atn2IWp1=(TW@DNTmO)2%ODvAs9NT8a'
    'Y;IQdoo$a2SgZCnhhwNC&vKPuo|>Z$peH5UmWAiSHIe`f4K5-HzBZYAV>ASHg!4)u)2&9L!)42klf4q%7d6c4!QZ|*wmo=7Jy1tN'
    'Vx3|t(5*>fr;jdbMT*&jplN?mXN3=^P!)jD@&O!z2f^0Cv+*<jcV0e|5wr3Hk-s{F6n<UQD6yisw0M5ZrN2ACo7m4MF>G>&#oY*$'
    'VIg7?&CFOPx8$q{(O>~t9o9fSWnm;lxK^i*t*?Slv-Yi(BlW=b<OS^L%8p4i0%aEU-4FshC(^y*N}_gFi?P4M#YF*X|u=G&tOMP7'
    'Bj$z8(W5<9dh2h`KA{T!E<OIS(|Ama6H>YD47-VROGm-hg;SDaqTFN`}rq`ErJ~J;xZ+VKH;J~h~-mx%t{7%ns~BmJZV$n(nDV;!'
    'EOv5&1gqycn1j7r+WakY8bwH{#f3@Ddkja?D=r2LCmiGAlU{|aal?=0{aI3rnubN7t&n)q<%5v1;NFZi7QX~NiwJQ6%xN#o^lC&A'
    '7zW`oN<x#R*25DXjAoMsuAkfF3}!S#5IZf5Ah%QN)6J4I)&U9wodsy>M$&{CR?pnACmARV>XzP3Gs@<MiJG`2kL3|vps3jg()P(u'
    'r8D7vPk8J!(NLYBF5`&wt5u~_jekl!K6t`G5m9en_&)VnIG5Qmt8Vnnbe-cUDKEKjh#H9N&xlDIE%`$Y;jRw%xdP`#h%#GPNfJwp'
    '_59f;48xj=Ck76;ceMc<{b_mAfKyHZ#xx*XZ$n8al$ZF^r8?QBLJ2(<Mvge{I1!|rlUbb5nLUu3)CPC$F+`%C)NBmY($-Ox*jq|8'
    'XQLl1BhB;Z*)|CooVDUCasi|P+wYq31T_l$=tcQdR%DIYDQxPjCM*jMGb`mXrT-)r0aCfalu5^^PusqfG5S}p&!Kud4=iiu<00!+'
    '#Fkpq5`J!=(D5NTMCE+)U|AJHK@h6z!MTbgX{_+>46Ft85>-W#R;BOcFiD*k99NC6YHBSvOL;L97xc)Jq)&Q(||@##+>t^td-;>Q'
    'ui@l?5mXXR-UzWo~Av~Q<!q^Qf#Blx+Bu5nwZaFwH}#FH2%+$o}?MQ2w-bN{4hG{1Ck$BcJog@_@Mb8K48bahaykqc4Er`g*Ul}9'
    'bF!@r>Z8g?n8vTYNSq{6dLoi@Rbt)W~3;V+VCg>Y^u5~=%dXFxlWiU`Q&*~^WqjtC3w@xdTaBiISq9T{2Oxu<WOHxk?idZ6ea&y?'
    '84k1w4&<oTI{sE*72(KA!scIvbbM{f-`Y9Pcdkgo_Oz$OU_90+~;D^YFcuxS}ghxDd7C!<i_W>O1-2V_^fj`D)caUET2<H(Ap(w2'
    '*ZWjb*5eEoNwD@zaj)oAvp-6IcJNLm|{KQ94E7F<oM%*@z|FMYXm1>0hhLB8j#?WINv`oY?xywvkg29RUGcROT}6^*(pU}tw_mcm'
    '4v62k|LZq(&_@OIQUA=tWynY*qFEE`<f`}nr~C0$`xC`w(3wy7I-A6%KFk81kg31_2q}G-=<aJ*lU7zSf;>d55mSx8TJ)}r4U+9X'
    '5AkjFj#;N^G`LKB0I=K%3UY27H`d3h|-x|HOB?2^jwql()fn2YgNPbvxyEgz4BtvIx44Drp^!NmiOA64yR@`g6%=6?OQ5y0bP$QU'
    'z^$lOrm%-pr&vYuGVLAb`1qBA^#qYSR<lRYm!JD0wyge;4&fVkm-E9c%q~o-!feCO^%GFh(f2lz6KOiem)2WCp}=EPB%O?w6N8*5'
    'B+l}!L*^Q!&XY%unj$bu7}hcS{69e?|qLR9Ji?=WS-Uk=c8u>Jg<K`8PS{nC2k(>0^VFKAKN8KxU!Dd1~aU`)BF4veMdd<JnCez('
    '+|SpGjC?Hm=EelC1sj8<gaiyuX*S$TwzseQ9z#tiYJ})9nfTxyjNgC=}o?+3H0x-oLPYyuzF<O^tOMaYr&JQ7B-zSwYs_GeWPERo'
    'XxN|Mblua#F<ek6*SQNp-!8MDKK~E4{Z9kfvBMN(o~;<X3Wg=_Bt?Xp`~GC{97HP?x<i%?R@kNLUNMQ2u)hR?wn8+DS|g4phQ_ng'
    'H$GN$tr^>;%T#I{%9s9&=Fshkk8inMVegaS2Nt0UoGv#wBjGSW(|O*%l?Zeva<{Bboi2^s{6MmuBpG~Y_PhzarFgB!&0_Xw9lK}5'
    '1W%8TItg90Xy@BpIduMHC!FRP8bE_IoBMG3FAN=THIRfQ#~PKoWP??aSXEEy`C84fy_4!TqJLWE1BsL2*B7`&e<l;yyqTvz7sd5S'
    'vbag;9<PFZLmZ8AJ$0)=F+?<cV+|qNF+YAIzeqx=akdgWe?FuJJqKH{ye8VVC0uw8NE`$@h;^0U`xqBy*7yV&aNC9W;9*WRsM>I_'
    '-lUA5EYO1hOP>U%iG9_$2-w)chPEt0nF@UV;q@x$@Mc8;IIy0J=q>7+K;Wp%wPjkq!y);%ZOdaT#r#72iG!``-tFeqIGcmVmR&t;'
    'EW+})cTJuXgW;Y2xpb?iDDu|>%$EZa0^fO<Zj$*Z@rOi%b>ulRsQVh?aIBCK@%u+od~1encT-8cXai<mC1JNZ?hR|K$C&(9>m0bm'
    '!-_-vUa(QE9>**_}%ifT;_JG+^7AMI;Q+!c3f;6iT0-n!#qtfopne&{Q)^A*uhn+3{FLJvL;_FCqEe$EtiQmCf5OASICB<E5KZXn'
    'f|&C24G(}x5ZI8ACLCMi^cfK0jc^>tEMCd40j_mkbm&MLa6*T2ju?`mtPeww+Xs3SboX(|9L{??`WtTiPxYry1G^6d*TJu2|t--p'
    'cz;sYMV!Q*WpXkx(jQy$IB+z3|!sMiXDFw>@H<9Vf+C%%q!na(2v$Q>x^3yYaw$%_e29D+a$h@zeKPA`u}u=BZ0$0{_vOQjDCc#z'
    '}JHzr(<gv$uhX`K-jfZEgrIso)1Rxb0E3II2<1_z!_gkJrCbbR%{TRL2(Mt21$ZzAQjEghRt2IOm=|7!Ukc{@;=IsnPFGLt$r#dW'
    'cweC(<_Ny3}_!=#VgUPW$1??Qx%tZJ(Y>I*{up_-Ue*Drm49|I6JH)V}SOz-t^`8?H%pDf3JS%g5p}6EhlbG#deT)sZDEfY9O@9J'
    '&9<y`r2gI1n7%woHDIpJNc`WxRpiiy50$e(P-)(*hpPpXUCiybX8?PWu%>HS=DjLn}Sf#4^H!LUgO#G%Ff*HBdxXhsmi8?!L`7Z9'
    'E3>=dwCJQttVRZQAi>?z{da|5dCtDO3b%oX7HAQZ(ZhL(M$9jt&vgrlsFqRR>btua5*_GVI}JV10CcF9VjIjeGNkA<DO^0Lg!AS2'
    '6+rssbR{-3PxR8W}XGg6t8v;7yB3z_nVe`m4P8VpVm>x>|US1nTNvFk}GO`dUZa?O#=xrV#Y@MHbW{s;*P=`ynD*WB>$LC(nFk2X'
    'Eo9=LBS#OL~5Z}^d&f)NsP(xN~!dEvQNx#&p)!4aO`d@K(atC`=x6JgAs2OzM*r>A(XEQ98VNlJ)ef22+jl?S&B=OB$`;PJuXM9P'
    'f#Bt5=#3>*AJgbkV_@HQM`XQEoqsr;QXq)pgY0WzH<4TE92P+snHfoDAPv@D-8cGBq=dJy*};bg`&?-F@fl0fcEF%PDo1p`n1M1z'
    'an;zF&KdTx;~7SPKU9orP-Va^QT7BX;()U2qa5>k1OxdH<}ery3J#G*!?Vwtoq^_?gwspQp^~4Ags{=F@mu1jXRn#S(kb6O#|S!4'
    'ZTwF(qG+QuI@htF@u~z<)IbPB2&o#6vG`Z040Km)y(RO8bqFzQp3hGXvsNO&1_J~(=P$a5~XmygmUZ2Wnx18+6O*Wkbb$;c~TH}-'
    ';@lV0_Z#LNEEWDwoA(0wey#X8I2M9$aJzt=y=I$F=FS$o7iMLTX1@(&yt8WZ`lO!P!mW;n#2*i-Azfh?PCI|6<EIq_eU*r3~}j+K'
    'eg_j+LKr5OXe;6ogS{usM8f;5WAVDq}iM%3M+@NOfrRv5O9wRd+8>kose3-JGlKAq*D3FRkMYJ<|!1}>YNqN*ikB!{GTwz^z6tdw'
    'V-8|TM-yO>X)hbA1AFdT{E0Wb=Is#U$=_KKPYfk$$3bSud7Cf>5_B{GLvlbj69^Hzjh=Yy8S$h92X@(PyWE&MMNd#n#b!~9DS0H5'
    '6-mgaNmv`MIj@7@(Nx-#VaQcrqAU9+N)kH0H*KuTzJ64Ek#b&cqd_Vr1T{KLWORM7s>2(lt;owx22`+uVl~GVxDSjWI`ihlc^*~2'
    'AUQTpMb(?fZh=m9SK+X_VLJhwOC?H_JnOktoYGUfc7~|u0Q+lD7-U8q<U%UgwRoMJcd~fXL(KI<PwB#D1*@8(+HoDlz9>0h#S08F'
    ')gkV(TQq3kV(SVt&kRdJQd|-tfe{%_ZuhQlob_ju0Qw}M;o#k(wUgW?drj(+`~A_UBKev1Pz3e%4VI~KP%w&db#PCiBi5^m`<_1<'
    ';!g}of4MSFh7tsgoD6Xzkh2&u)`DW%wf&3*e2AY7UWo(i5|uRx!P){i~4<1%%6_SbR3f`fp^4Y#AVya)6q85Ag3*xq*(ZZSWq=zn'
    '0~mr`<2pxz!T6zYhHnyB$sFj<pXmCmzh6qB=ts6sLeb?w>}d?aB1)e-w7w#{Q>X?w_<+!!FXr`B{`Tn`NauUTZq{vesBM#O=bBy*'
    'FqpPkE|U$(=nmt&*@O>$Pp*yy@7;V58B;0S*mTd-i_TH?YO$6j6b!=i>h4ZT#$-hcWJnla9b7N-lat3aB+cE9G7jhci-W9Cc@axh'
    'EYKIH{lt;1HKxo7H80{OxK+LN)Xu|Q!7DziMe@1a(vu7r1}2~cY;HopW%L&&4$*^For|^XBO2PqlY5om<&(A2-Bd?^<miX{KzQi?'
    '59loFe#ZzVq6Md(|lL5&z<FU6@)Flvkg)nO54|1`>e(Y(NQji82p}tG<$yV&@sVlMoz3Yssz<VsYJWsE3l&&;U^O$2OVl4XVTfR3'
    'Qc}zox!_fUpTWp)re3up%T5QU~<FN=fLXE56LO0H!?4^T^Y{MP&>NQX;L^3p&*ad6GbJ4?3>X>1%&9K*v633<{O7oU3(6nYG@7yn'
    '%k)kWK=GrB2Xs=x`1gh0C7r*0qna{gjTYqq|_9cJuu_M@973ZJPZyemQ|QE^<qksk%18UV<si3@VAp<xRhj$f6N4=yRXZX{{3&7-'
    'g)O;x&Eg=`-%PQgP*w1-usFF^4In+HYs-0xQT(G(%$&<)x+{_(opvdyTH*@rn@C-?8h;I81;Q3=hFC_M0uL~je<U=N&W3K!k7r1w'
    'DEC8+4GgTzALV8tbcJ8BR&*@hLx_+g4NV@We>h;pE@r|%mrFBKs#7#a0-+#XrOIvAV$1)wS<!zX8890X*4zA;~8F~{_g#Zm-ErtE'
    '|r!iK{S73L`jhhCz$bFw0~#qvt5bH?c5D9*;>1AXT)&J<o?hJmRhmoEGPJ*OviO_sCMhu;9NyP%=O%@>}RcLk}V&0cC@wLxbs%#{'
    'rBF={8LA7S9|V`T=Kj3-$K29kC=&X>18?$-xs^B6)Y3!Y~QbKNI%t!)S)_`i#BExLKkkEOPX1SU6R?px_Un40Lx%IWI!nRp4mYdO'
    '_onepo)4cPf_Nm8KD^{ktj2#Us%PO>qZk&&!`ii^{6Z{wNhBE*z~)}1$zyB1~N%r+&>=xNd7;cG`xzC;6Gi;5QT)J13qdTHnHb%B'
    '}c-y=nNg^@zBncjRUU82G+HLw%G`JKc`&p@7w{J7Q1f%{muD5Q{-AAZd6zL{?+|r<%@G`5wf#U+g?v-;U;E4gE3i@d4$<)xvZcbQ'
    '5W3T7y=5hIR;S&?u#VF=*$8a#~K~bCYYA#v;e&#Thyf~q55)to@9e^&Xl0fJG8yGv!eRjn|f}^3F_$R6lJ4m&qHH_<-388?Q|v{I'
    'jfycIlsQ)Ubn$C(qu$ej>s4H3#@EIc_T{6k2LcIRQ9g5{38D#(vytro48?h{=o>s$nr93lOgM;cAFhHF|VeVNsJ5|4%b)<2w09Fl'
    'Eb6`H$|&m(~Ax5AWYFY6{iGNBi1(5>hi}qAJORp5lvi(YQOTdYI_;@CG!JO|Drdgz2QyL7lYctf`@MO$TM(E8zgBQ^F4dO&SaSEy'
    '?z9MCO1VzV*noL_h22=QV%q>zGLE_v%+VgivLB~>s!1%ZD_uN*7%d+fo&zcT4tdJ2D$cJRzAvGTZd3xXWXAi5k1_l4UZFx3|pKdv'
    'CcT-wf<qSveIhRM>FSZ_f!NTtVDKymgyo$j5j&D8@=e$vE{8V`ojs}0FNzt&Oi`vMSB6a;{Gk}`zb~0`N3}J`IBk{pU?snM~8rTY'
    'FSO18McnSfjvv~%zm|R+|qYOAU$D*zUp2z@lV+}!NP8<$Bv3Q=}ifOg|uI%xrn03HZ0lbmX^rq@p<!27sbv0`W^9@>3eUjR$h%du'
    'Pd)~gB#W25`4V#Jtf?n!%@=xubxoRiw1M@xwADKqb$q#lgitOAJ_KSnUXJNy#blcK0g&5M@t%MQt~@H)_8_k{o&U<sWpL8avs$a3'
    'N%p-_@sJx-+DH=m`bg$Ts^zr*w4<#GQ1<mQkKTHu%t_seJKmzIi&rc>S-F9H*7je7#sMH6(FO{mWlu~J}=VOOthR8ajD+DqUVj4H'
    '0G^0kXBtK9fIbOh)02bW8*kyqt!8T85QBI#ro?SVZ-33nRWUQu;jBr82Mt;i3(AJ&aLbuN$ebhzjYLiCm-<eldlx(Lf%Yvj?fAMr'
    'VNvAj%xoqcqFlouG1GKO}a~zK1pjyJz=PiUHgJWw$)_|Mc`!?QBi|jYp8G7768tx>$~8Yki?!-<q=Qj<E6Vst)kW5q?J_2=(*<U%'
    'CLTi9z=tK+?Bz8MVl-~kj3Roc$BpD%OH9RA3YPL2$y$rEc5ehmx2MKL7BecOFHoVGIzV9>yAwD&pSFk)or#{-G2sigF&6rLsAc06'
    'E(uH{5vPHr7N5+4Q4)LXw2_yY6iBfy*M6Z+W_h%xnWDXWI)Yb=K(c&6r)$;O{P$A3&qjkk%dLgnDqPFJ}QyAySKKBYj&HIRUrg3y'
    'f=2+wrDx)-En3mG5R#^bfc=E?#8{XP5<26dgIf}hr2%SyDS5qaQYDJHX~{q7122s=m-U%J6urxy>I2RpWSIRXk1{&F=eI9s-D|zP'
    '2LYoe!-UH*nKpj(~)UdFEVlWx0EJ(URFQ7-#ftY)^xi=ZXDi|MiG<jAU+?r4$RcGNrJxNhUU*ID<rZRGII_04aFh}QZBiXxx(<gZ'
    'uhoZSCrsZ7A87+ro<&z2}#fBPv<DKj9EWzK@`{7zJ3(Fo!vcMx$NyGknGt%U806U0gyJfht?L6E;2C?G&X!(%=|(e$zO^inPG@mo'
    '6%0xKTM7aA_q;`o7qm8z3yh`eg+1wdI6)!@ns8gwH+QD)t(is4ovA(eQi2JPPINoW~y6QZ)bZd*&LYUd7hN~w2Q|3ICU4bF|)nYV'
    '?^{j;F}gsy2K`MlTXGf<^6bcoqV#CObjS$2MqOSl>ApTK>c*sn7YlK>TX6SpfRP!oRU4`-f=#e3n*dg>Y+K<DVzPg2vrC&p7CLD#'
    'Vn)Bkr>E$X0E!s<23}S!-KiC=v@D7d{Q7GomPk<i<EM?39(XfO@~9&fd-xikd<QJH~wmNMNX#2B8lw=1t}7we9DoMZC++VEFM{BO'
    'wXg?Kz#tNA#>Lcl6E`q4$MMELRUqtp_H&x$y-$D?5yf9Yy?iOYa9&Mc_C~~VRetX;7&mdnAX@UU>u`cinM!(P)m6pTmo!HxLiUWc'
    '={loS-|N!Era$-2kPVBXZ(#uj<!hkiqY`*h5PZ$Q}31<X}Z(;SW^`!jFf`|9dV_8v3+Dh8$pAx-40)%TTeIw*8yfiO9s{aI9+2^D'
    '@}7)3P*!P@R_Yqm;z^j=E=p0(ksj2z34nmxK0|V6ag?UE*2`A*2tVaGH4LHV6M_hmgl$}j;w6Vrx>isD8Q5YtJwpw+bl2#LayV4|'
    'Devpt~YM3bfz;b@!=L>Cx^`cHJb_9vvGvduDTKW7q(?L(d?4rR~cH42Y}5b`GoSmfvp=dmN|4{nkbeWT@h=p$Y6_Q(-?;tqQfN<-'
    'QuvbGg5Ls1ENdW^%|76wzGyxRX2aQ0D5>u$YN4gPDI<g9pLI%U4=0bq}FJb)1j@=wL6mawz2L@3JMZw0w0XMWt{0$Js5-l2us*q*'
    'J|ZO&T9*iN!o^C^I7F%L?0fj&pke12oV*b4*6hm7Tg2aDe_+Maa03XpC;E9xWU0|{`I?f2$@whh68W(+eL3itYR)9`m?qk5AMe!4'
    '=cN~Twc!t#`}9oS6Tg1F5p*R_f5-xeA8#cE~z&59dubwhVzt;L8-rt3D9}!4~xkZV`M$ywZ$mC%8(IC*4_Y?#&gMHEE*-wwV>!s%'
    'wi98^d^<>f1a|f8Zw^VYObx_uS^by@+dK%HPjMXR?diCaCR|6Db6lVEN~Y}VZEcXjwyH4T~?Xy3^(87AZ@`h<l>T1$_kKBM>rGLG'
    'N*f`sCX2<KMvIN7C{OgF@wRF2|#yR#B+$+A;bf-(tnPO8fpzRl?<lbgjUliLM(kYu2^+d+!_`Oon2>rHT0o2240QbL26QDP9K9NF'
    ')Y&aOc4nSp+znNt_;9<2_9D385-T57R-34ogj(!=863@m?<;kBM)WMUL2_i(Jkk5zX^?qYA(f^Z#r1Zg!cR%qL$@>su%2+o>?@7%'
    '=}OdPU3nr*KX<LsY%d5ob`#KQARX>hW^?E*UED+L;4hw9C#%e9fIo~?vQ#2J1w5n*6vj|^YQc<8i}kPh;QX|EJYo-jh=Zxwf2Dkx'
    'RE(j<%#K|j8Ca%4NyFT{iP?^1Mqw^Br*dggg9{b^ds^>8eQSu3(?aKmjCTo4;|kkA|M*cr)JVn5d!ma;;>evu@}iPw<W^EPn_Fcd'
    'P+iL3)YxX*&1u3w&a!fd%*T?)k8G#=E15KcalEngmlX4eG?N1oJA!8`M9+$7jS?aA6T`})ifX;rD&((qC9Dax+d(IK}g`d0IkZCo'
    'txZ*J_JoYx!av>G7k204zCm6_j*ozc5%}_79XeKO3%eHHEHJ0{n2W1#Jh}|vE7C#Ps3pPSkLR8EC@=2#MlsbRfU}#5>+kAP$Ac&*'
    '%HAk2N$PvdafR$Te`-z&8|~R9Hzq1-2b!P+r#DejfE^7O2T91;t;#m4yZ$F<Fq%JOxi+kPLZ>D+SEQsi?Fzi|H+uCdE>Pjad};@&'
    'b+naV=rO0T>HFfM;mfblpa(Tjw;Iwm4!2pxYgE=X-|~FCGSvq#|ZbD)m}jiJjxnbm&-;c&w+L?1rOEbC_<E(DKr*`X^R2RzpdiNA'
    '&bV1_Bd@#dsA9>!gR`rA&%x?(05oOWd(V@2v$@izafRg44UYvZzro|6DJngGZ1a-jt@f^z9!GSPK`P`v~gqec|GSz^LcG;9`u|4A'
    'sPijOaYuLT3c(`VHPcxoSUuzMK<r%ZtSFvFit2Sh!M$Wt0hu#xa|6$fkp{t=6XkO`^{|E9qrTE)zOO<14&1l9oeKj8wfNX9s(wHh'
    ';FS85__~BAzy*(oH7eBC>|zTTN^?zICl;VWu-L2o{Nl39ePQa-LU+Y&&;`8otzxhUdSFi$X}FJ1Bw#w5;(vLw<`cK=b)k>SXHN#$'
    '+<9H2iMXi=R8e^w<^QPtIyccq;SAzKXM1S4dF6MCXcrcUN%YKF&g_V5_o~_5Mq4rnWA&e7MvZVY+>v*=)I*uK+PIa{y0iUe5s@PM'
    'q?K5Pud+B)*+6M(tum_rvZAsF-vm}FCS2x5fYlt6WUySyF6h6>!lNcJ^FH4&SL;JwG^ein&#p{D3Wh{>CI~s^e%bGvH5A^y#p1+g'
    'zV(9WfJ%|H6T`sWZ=#d;#!C7=2jrZG>t9;y-3eF0k2teVN9cYat`Fp`$l{-M_68rN~w+{^&gO<ipQ5BY2J$Zw*)(0yo?v+q~VkPR'
    'xyCdPmo<!)(&2OX`d&W<B6*l#K2jd=lwL~#Z>?>I+<~abNSW#?<uL@pzod3c;*tn&0Fl6zmikv?-Wm;S9Tux=lJYml6BA>l4R29;'
    'JGwMlKAlCKq_YBhVQ?Z;fbw#302v3H%`HI9V<}X!Ex|`3}2c93}>X_uE}X|{wR%9pZG}5$4No%1l*mTH_e@2^8P-3o@C8Mb$cA3I'
    '}<R1_ohA0fZM-e?c`~H^x8oC1<0k3ABwVw4~O7~`KD)(R^bfdxRR5C+CW7^9bil>m*Wu!0HC(mA@SO!@c|;$fd^i?YwZ{}`+b=K7'
    '^JP5z8_V`16$F_O#E;XKR<aWpWx>Qc{%f_4H6>`C4651PRs$aJ=^pPj@kjD4C3eo@>MccYXqjMQyU_yWy}fTBBH`)vxu7?A6O5oa'
    'v_@3$vgd$ZKYKbuf`+CET|9bH^Xg3VO)7$q$DQZ&Z&U`-0J@0c=AD|073Y{uYT>p14=5dg!UkR+mj7eDJw6qjyQf<1~^CWW`%GsG'
    '%Bk-RM_=AFhWAb<Rx?1Mor&cb%Xrv1+(9lby7J2bLbA_Fv7bdn5mJ^%#G~r&a4<adK0uL0>i`uATX+vhtcku8_k5xOv-k$J*)^Xw'
    'P@7JT&!-K)9Zp*cabn%fDyDO1j}l_7g|121UF?-jS$`ch~&y`HBUh>h&h+I>1nw#l5Z9dLjI2z0+F`75s%J*4)8Z(mF2pE>ov}7*'
    '#hONrE}M;&YjX@qPWfO<a+y9{i}5rboX+ttYCNkRv~o>)qwU$uz{-{MmOs6m~TQ2bV9tfEgO#0W#MVH9N6Cic#auoto+%g=~c-{q'
    'rOY+E^ZA2<0391cB_H8^}59^zuz!}4ojq6YG&BFSWzV#{G75zK4eoJc~=rtHttp5G9j8+K}pY-&#scI+`}HC<oCG;sPP>vNu`hD9'
    'u9W3H$50M9n6zQHZiutnApYnE~@D-3yW#^KX3ft4Uim%R_Z5qKFB;PpXtzCv@<I&dU5p9gE}F@@_R}TGGXATqe<vaKVy8bIofhZ6'
    '-ti2*;{igSWwMxm5wgaE^otbHeR^;QMi~7yR^~_87^VNvJ;-_bQgZorGaW29ZhsbUl|={98Rn93e1nS`LlR<pf+<ZK7P=eNbL@v-'
    '3qOeBx<Optfe>akK!bYJ!B~X#$yBf5?0&jhT}Ze#Sfz|ZV4K3SeeS|m$lg~?d`Q0kDM#u{1ob+&cB}0;nl|Uzqf!rm=G&VD5VBi4'
    '6Xu0LS++Kk>ww}F?BF+;i9oV<|2D?^kL@RulpYUAcM&-Ds0Gw;OG}yE;+S+KX}`q+denyE9B&<a+Qxfx>yL1JBxdaGt+YS{WjPmN'
    'cPJ`X($LggZ6kt8=L3%#3~?}h&Djel;LAdC!>o5+3ywf<A^|Y|IRZ*>S7$0_u^b?+d4#8<eY7t+gTYdMHajfKLz8F4Z400*Di19e'
    'tk*Ar%0RH%wlx6py)fqc;qsii?0slS1#Tl&KOvWM<#IK=eF1hIG){<+te1sbr`G6%(=KdP!%j^3zuGwr|loVxYh@yJVE{!m%Lvjc'
    'X#G5v5K+m<^6r}{s~Y_Y6I3w@x6k)B7udj0D6P+sq*ryvfBrix#<^<XFDyylJt;RnOflE&CNzQujU6})6!?x6Rw;t>YVL7^<I1?f'
    'd4jo@UJW~ag)^A9v#d-E%Tm~G-cRn7vs?*tU`ZSJcdw$S9hMVnM?=@w%m~0-5y%Y#Fv+pY>;GQEj+Fb7qow+`5#GMR*Njd=6VRY&'
    'FneEvB8kptJWb{O;m(&4QeccD->p~ywKBIVvgIeUE^P){?<e1AqqT<8Zfm}$3tUIo9^ArA2Ysvwme;-+XeuookHR3!kkaN<6!LqH'
    'BJ`hI1)JB;ucFbO+ah>3lYPCW03H=7fF%__?yWCZRV)kz8`&Rr-rJ7)41;!rTEoC<~=zDgD&%t%z2|o*S=W>c~Ft8*oEhnSEDJBZ'
    'bFf91ckAC3FP%@U1TdoyOPpji(47G$LSa`%?9n^p5TwEXMJ*QBifk~Uy&ye;rRSYG&Fm0YVCC7Psa)xFsCq-E}L0*!gn>>UW4Xji'
    '9JgP9<m$nB(d2E9uxLkX~ET{7B5QEP-+g32^C`CWCKD~zj}n-J@Qa%$|nu#JTr&ffPz?keN3k1!w=tn`|aQVz6GsaYY&_>vTkUql'
    'Z^Pu74jsyurX$C?|C!VnQOl(uilzVz*6vp%cqtNP;?RUB%*FeO)6%&Mp45u7i(9nR&}7_iJw*UYcf`TYT8GGmEyCZ942=?1JYkef'
    'k$8=*guZe=1}27M3KU9=^9e;*<2cx)O>h%6N}I1l!mw8H!Q?C;ssNah>0siIEkJX56vLtz-2S(*gbTq4!;|u`x;@q0T?Q{ZsFWO9'
    '_*u19ICAJTY7U}>_%nx(4Zxd3q@abtl&2v(-#k$BW&jQux3D7EXlDlfVT@ty7SGthr)z60^_k~aekXcBtX5>1aWhttZNgVxjrnCv'
    '%?upj#W;+V22pQESMPx5t^*)gub^Fg155rEAixrui+9M9BXexo^g?OVMU=>wmY-R#XTku#`LHr<%UDH?)*7U|KJ*XM=NQC#=0M$R'
    'qx}6wdarH8F;2h!KmDfPS-?5dqZq=R0|Xq2(fp}??LRic>HzWGx?7UkuWm`DS1m#MerIhp+nKa6Qv2C@b=p1Xm#y5CcFR`0pb)!a'
    '8gDb1xKySZ=F#F!(O!q@~fh0*?`r)|7%5p+e|BEfy1A`KVpAP*Qv*vz#;`%A}9@lcxDjwBB<=v$olC}*rr3(I*QqRJ{N1vg+@D75'
    'h^O~ObW9<5(|seJ@-PHTvWcDqwnrmC#=0q?26gsK<=y|TfDwsdsqUX;qJJsHEOK%F<9=t6SQu4wI#cT1`%0#ahNs3>pv8?NZu8td'
    'B<b6RIAVBU~+kIE3hmPAhTt_?Aevd$&%96jcGMOe{Jat@i9by@(%J_1W}IjuJnNA0^|J9r5t^W-A8+&=(ozeRk5#@4VSA<iUvf)n'
    'neu71=~slH^FKGkLbMEU1r#r36DnM$Y%xZX7xj3Ltw8>NIQj5G#h+^Z4en+)YIwk5lK5zfP?itJWhsSh34ON=ejZ<^mKIHK|fhqC'
    'f04{>7b{%s|zS5h0(8O$mfI4`C)zvsUK~{fpT!&)hI(b-_r#3goTdeC4TX4^Dlm&(a|y4LY1j~SLA89KcYc=)-gm^06C)<i&BM2q'
    '5(4%^%Pgzm|1H=+3z>7C$PK<Kl0tum37d0H7LGKZt;@H5Np0HeRlK+dS-ZByD5(Z2HQ!zV>OsQ)NHkE3UOjf+Gy|vxK9_C`CdkSv'
    '5R_@x<toi*?v(x<naXX_OaFH`;mV`SOmC(AV{8WI7~CrB}^+~CWtWd^f{!v15Nsii_&wCd{P`<&<Q1;d8#{o%M<!33JkSa(0w{X0'
    'Z2jdGCuPY2IP%jT$EeirXqNhRDkPrGH>ZJIxw2ewA<;#`9ez#Z-|qvMlU*E6(?Ld8?eNI=+MqY%rqGOkO?$%QM6{%*OeOn)>V{3m'
    'mdv!0QVK8rMP_euFv@T`d}Y%NM%%uLs9;@TyHL$Qog_~-3y9M$2M5cMo&fc%Q^J`(c|VV>JsAVN~092i+KM`re7s87FBaa_l?J%6'
    'mfgby$y<DVe6F^e4HS@x6GK8Ul!l-CUlTlKL&@KF;m^1B2gitjg4}gcjljH6Y*-s)t;uZgUJk>_r?3=RNd|A17hPZFTmAZi!rG6r'
    'X)+K^-%hvXXK$`G6U5?hg+4+W%<y?pyw4&4*~+szPl3nkO{lyYamkhxQM9yDX0aRGolj<^Ris>^W~z`7IbwP!+}c4co{r*mARCqh'
    'n_Lia36YeA5S+>g}aZF2wFrRLS|t|4ITknphAHL1nHic+x3Bjq^AyeKo-'
    'vMLi?MwfwEXAsq+sXP1jGq7zdlLE+jv!=M7blt}(Zp&TDE-EoL#@I!VLS6bQaabt>1UCFf%b4p(vuErCp?BWzS%=Scemua8L7)et'
    'ihM`eY=&LEY}5v7ohVMvX?2Q{~t@D4V?RxtIGlOU?xruG;TLsY3!LD*5TU-W}gkVqr<(CnpQgXmAs@7ESzg3XZOFm3AN#;n`M3Sr'
    'g64Uuu!a%$W7AZ^$uGS$KE&sNtt5NXT56kWtt3{GqN#1u^xy0Mo1wVp!fPoQahZpqP)z)hY)=ey7}^rp^$u?GYQe_zEwmK5J#%0;'
    't-dItDss>~B3)ASb=u2#yBRvnrs7=SkjYuj65*O_K%AtOSj>F+XVPNlY{`L$|R;tOMDV6I~0)RrNzRdEqQX9Fj$P-T*Kmfp6}JjK'
    'U;I3W&SZ}g-eAtAYNbhafah==SEZ(2QLI9#RW=;W?9V6i)gCqw|?9A9F^%E!NX|0h3d{vRLw!{0Z*|BwIuXCM5w`Ga4-|I^<9$Of'
    'BgXC3?Pd{s7Ovy7Pk!Z~@cMuNHO{!&yph#zU`dI@A1a+;&PBY;I6Q9NfhQ1NrSwf+e<sjrM2$GfBQZ-dcVvfld)dN6&y7hp-!z9r'
    '3|v;&b&R!vmq)Pw;-lsu*OI8RJuybO~YHstAk(zJxPBAcc@h@WJvrVyKwwBnY1o!4Hk3e$1DA9>e{@&u0X^RakD5kh?cWd+rZAGQ'
    '0M#*3qeV%Ay5?ahAeh<s%s<L|%sx^L0xfn9JpZs%c_k_t*62n)`M$q+bjYzgT)3FR3Ss`IQ+aHY_4W308$scC{xPh(YFT^XDby0g'
    '3wBmk}*<s|E&km}Yrk`sOm0$LEJAKeJFGmVywp@R<t^6~5@=y)E1$=Lv(nyPdIWLRfyPI<y#6XN{+_g?em_fysE!A7240g8Y3jO9'
    '+LsxsEa)rN<~=kV2W*6kpyjuXR#1#-o5JaVS(pimvxO#N?p?7=*9Y$jduL($P)E{*90+Bq5@Vom}Oso{NQ#Asq8+Pc`Mv_Y4Z&#B'
    'v7S2$N+GAEu0Jt5L9c!6wHB>>zRd4koH5%R(2iv$B34{;DVWjR>7h>Y)P+s2iWu{MBrCU3WRwpP@jYz)erhHc$UKbSdw#85QnR5>'
    'Xby|^-?s?!W*0~=6_)>tNGF&?E5x9T-jt<j4<vNOOURZxucp1oP88qxHS4~)yn>YuMo&&j)9ln<>zlV&FL->BI%ESP2)nyY=!Kwt'
    'ZSm0%E0NMcwkpcy5LU|>`Iaun1}%*g+Gq8beT_*JwtjM#d9sGd6Ul6udjxf=R;x`I$=ZIg8}o>-37U-*Oy6FHD^OHY7O=s%ayX*Q'
    'dg_R}(V2&Y_30l=A@Da*H2PykSo7Fn2h6-^Aw#JQqv-K~ZPbS>tX;_+3Ekhko_>*|^jBz-o(9mAYA^PpmecyWz_4iJt!hb4GYQc1'
    '4N;!KMI`oB<@Ugb>)kPB0%a^?H)Wx@x^H6rzh$4VWVzi>7ax1;Jg$BttA16Aoy!21=5W@ma*q;i)}7vu4M&m@w%E)~jD?vNBiqOT'
    '9|Z33V$sEeZhf|xIfF;6X^RG!<KKpjZT4TQ;PvGkpg^xE)RE*@x9+2~oZA;L^dmUzEkq?l!sHU7=FVRP;vNSdB4mJMXS9(s@o>kC'
    '*uK<1jDx`1KeyQkR2>+^D{udPhP?A=$8eC}I&4e-s{hY48~mrG0Skyq9G@WG7{1(N1FwwBB0b==A<SXvOQWJZ6^QNO0J(@N=dw#i'
    'vWVePWkBaM56X1CSeD1nR%-t|+jqQ7WCzS@&_)nOQ=Achfa7#qOp5a<P8Z2@e-wIeCb@c}DqqWmyag{l+t@9j*i<EUxY*I_{NVez'
    '<?t=jlW;y)Q073NRrIRA7q;&`S44n&<0%oqdf<B!70F~eO=Z7mc`&JCOS%Ar#PbzgKgCg)Sbh;{&)#pEC|#4iKlwJIhF6-e{qYeD'
    'k4hS!yvj~!8HoX8z4;eJIGV*-xW`^&Bt(VHZ!DS91{LL}zJ>A96c4GyX(LAM|SaH(D-{1n*Jl_NEW(b>I@>^<3Ah3(qXN_e2+=`)'
    'nf{n6sDnB0VT@Sa6(RrW&BF<8Qu;wnffuCdq+Vp+!7D4Dh9N?Y=16H{L~9jz~;SGyiSf4bu`qU`j(te;*3J6@8d*ETcOe^d*_jX3'
    'Yas|x&o|Glfe`9660n6psI3Qgq$)8fz*>FR4r5z<kvFOz*ypL|(W)9=4`*{9$CZqfGSQ^$b=g3j(V1~Xxs)u^p2WW6WGhanPio~F'
    '=)DjGZiI(&AMBv{;GCS#a}D_C#Y1Om>g1WoiMwMKS~h9=^X^LT8KQV!of829+(z`PzGVO)`{3TDhI-K0f-^>dAdV(*a}j2AQr>M+'
    '}ofQ!QDTO%x;p|Uz+AWA5WKqgdzNgYE&83g?t767COpj>%`53V;>>hTfpzt`+b8E7K7nvJd60;0|F>7qD+zP<tQ&6WF}$BynlJ!S'
    '_MQq7SrWOz-3<g03k5Cw8IL}c{goqzX!v#(^}fL)y9P$uP7!nNjroIM?%=GuLM1YAK^7hwSq>e_TYLmghrm$Eb`fLL{fLS(ErJ&1'
    'fFIw~X>lv5F}?O9o^#nZTR5k>|QcI~-sN?&)cPi41o+lcgv^enLq2v|HV!?R)_W35N5Ka6;W*m*9VPfn$M4D}#sJ+h`1C=lWR)n2'
    'A-u9M1IN}*_8wQz9~`;7&)jvoFJkl||iDqENp?FfP$J)^2s<Ld+|1(HWCoH@cSt+q|Zs^LJ5<*6O6&3_b(++UZC^jn7e!S^^}b*F'
    'zZL%WKFUp@#e{Loydq0U-Q@j!4ly?J<jSXZFDLPtm3-;|6dwLA`(98a)y#b4BvPQoOt(x^5(&V(^0JpZ^dvjK%>1Stp%t5=GW4XZ'
    '$U!qPnuE@La4Od~#3Kv!>ctuXqAyl$OIghqy5dCDS1(Y^43IC?Py=7>6v#7HFjHbd~<B_xl|O1V4%O5?0hWYasJM!W>-%>X4sOo@'
    'h00d1QyWNkoTVtIpgqii05&v!w?a0PHye@w<+4b)rcP!sAFfM%>fwmMwV6o3wHh%MQI^>LI1BLqfuRRU?UO|K2L?S(XE{avH%^(1'
    'wwoO$3>{vhCZG$kt^k3O$0Lcqy3t)AUQuLhqoAvrvQ-9CLKy2$vkwcx}Re35t$Z!9WgYSN<4EFc*mG$CCx5(a9*R2Cg^%#&`ya9^'
    '#1zDbo}(z5`a;_Ao59qA!#XxH@NYj_cU|Gl^0ZvJNagzp<^v&oy}or4#4PkSx9LioJbUYL4f;3o6!AqlnPJlQpr-^R!+sO#Wn6Ni'
    '5K5XB4hA6+A<uy+7;21^FGA2X^pGj;420SHkcREQo;qFuGepO~pfP_-kCF(>){XmtF_;=QGr%9i%6-u;505wIAc`hCYpy2sAhV4i'
    'NaytI+R5XR-?I2^L`Wx3KIUld5QK|;U;sg_9^PGaw<dcH^@qJ<JhouZ!5h+35ihl&CswP_1DzWXbi55!VH*n4-}=ivI_)EM>2ydx'
    'S-U@yMD&@`4fN}7dnlIie;j4t`TE8zOwZ+_88xPmmy)H8o0al=p|Ja;c?@y7SkI!fHpZ0Pk(5y`BR&ivp<KZHXxF%#uy$x2=+J<I'
    '&<58mng1Jco?9C}{#9v?aw%1w)9&Jhuij*~}KO>MOll?rw;9~a@K4(2T#g&>Ras$mlN*VH5@fB|S99gf!bqr#zeq7ffa{oS~45;y'
    '{Py=I9yQ*3g2#^kLhx#2&cH&raxEiTtI@B;K+H8}um?hLweP3C4=e%#%A`*ybL&fm3ULf#tU261q7UxlF{0`0Vv>{)zwkp2GRg?('
    '`=`R|`u<}*P}%26H}b1Vf{vf!lN_)wN-CDVegE-h%Dpte0}1+>@lJwt%Yv7BB`Zk9oJk^lP98=rJ_^mcyqhTPj2x=Ih?M;jgj#)1'
    'tv9Xsom{#&01eDvNt8(gxDA7_<@rZ@-OY3q;NnM#;(EjFmfk|M)(H3J=pvc8hfv3kBEdcX<=Me&!W!?&)@7l3D-R!%VOMdbN|_@~'
    ')Tj$#U_vW0!IiMSmaisZx!S|vl{(41-a5hWK=AJktY^WX!iZ@$R@Bk9RN7o#=++1Tokbs>I^y&DnNcIY~XyQ3roTjAb5V9EedJwV'
    'LY^wt6&Ya3Ys4%PiF4vypu4q;bkd?fKW$_a1Z(m+px@4`NxhC|WNkkdK-=`7AUf2^3$5ht{jOjBtkWiw^|^6S2be}&>8bbK(9fg>'
    '&JF8RUMp<3(57yu}}`2<UD9X6o-sR24U`yx8IOAKnMbw-E`Yl%Y`83sz|wWwJ{@C-0TF-pv%q8-JBzc^lI!ilR+GH$KAeZd^Bo%X'
    'wfKvAp7%1^in5XjPm8$_c9G?DEt9GWCJFIS8Aflen{Q(IqP<7<FR({={5m(8h&#ckdy%%>SN#%0wgTdmERSO+E*=;f+B!<t$ZBm*'
    'ZJpk+1k#A;<^n3cz%qg2nzs4O*W#c_M?Ek#%ug58=?Td&&hoE1$J^N6eXbLA>nGiBcg)JQ#2h9C?uu7*B~dx#R(5<4h0Y#@bQbBq'
    '?B<mL#@Rw6B6;;B_^1xO@M3z^fDHqF?(jt}Jg0^zrPFM3hnCZ%SKR)%Dg(pL~|%47}Bd+6KHS10us%q|gvQ{(b@WicHGN#EcEd4;'
    'M^Wip1N{6Vd6r8+ks^$%a1Jgy$Ns-sG24c+q8XH)y_9F63ru~ys#SJJ$R;UViz0)CVlIO-G11s+PyUDDt{MXt-xv11!M2C*jWt8s'
    '+1^N;MwTiPtp6+qyEr9PuWV|N>pe`*<AebxpxjheHz8b&v$SOeZ&O(!4p&xi#D1;LKJIuN@8>HIUXovk0QXb>@Y|F;aq49gbly-h'
    'Qu7q{qD!sb^u&Ps%sAMwd8qQzH+ky%fbj~W}T(mi>kHXGAZ;AleHkwGTzB{r&(B76Hch4EL431y^nbs=WWur^ceNJVDLo@2j3c5O'
    'U-Ni2&eB@6W*`K;xhfx2rB4+_<&tM~}NsZ$Wq=>vpQj}O8l?W;Ln>p3I!u(;*wI1nLNkb}Ep%OOJ?%o{CSumqj~?C>Qg-itMCj=)'
    '``BV`+K;T+Oajj*6_pG`e4HBzy6lwy%e%Nr~-I;;s{5HCOuk3Up-(6Dw>lgZi<+~N>pEiKox-M2n!|0b5f&TP-E=AMpbF+@6hd(?'
    ';2U#l~J?*Q+;xMj)0uL@!cqheT}c_{;-H-5Z_>FsH=oK?JC&Egp4mq5iGvN(EEGsW(qf(MZ|ZD*V6$c^*6wGG1X(wYVu`)mEX@%X'
    'AyDa@Vn$4cd^wN;{h!XmAZ2tx}-RHS;E2E4M9he5+(dFbLcw}jse9*7$*s?)NiDKsBZ`TTzq_wL_OU1^r!zj7D7)}$nw48R7HUgN'
    'Cga%EMy+MSt`?wqO`<7ouQHkBCBfHTuQIlUx-q>#{)mp~6Bfq0`A2qcA&w3g>Tu@#}vPuYLMe0%TjoH!AZfP8d!uj(|CBJMr++;h'
    'J3J@&V^W;E)0r@U{W{YgHCXKM4#8^2pu;n{P|U!H3^e;%CrCr_O}ak}~ZH|Lu6F~xKKY|{ysb&4R#lt)=P&_A!#vs2Z`i#!Eb!ja'
    'nmPX?{5D&Z`bpDFYm-Gc~d{fXjv=vvh164F~ax<_t}LhYC_D8HBs$-y>p_l7CEOfoj-F?XUYg&HC96UefnjZU&F7vFF=%ZMBs2bC'
    'l`sH%qk@m2mc$d<IETGbUy|KpFZx_~Ljzf}BJCH6`F=iDz3X5b#0DtH@DClQzIF>?|>bL%>KBb*}+F1;D0*-@oTxqY%@Um<sO{zC'
    'KFV-*sIwILwdcrZoUiBxzDX$e;+pvCk{ooGJu#i=0dajxmVd}BTvbqG=-P+iuE)%hA+!z(b1Wycy%{`S=Q=5ya}`zm2?YWz$L;>t'
    'MnP|;H&nc-M&sA$%MP$=rGAz#?l>q<|_V=0X}9^nnIlVnpj`Te%i?NJ{hc~aan(N}m;o~IU%O)`gtZZJwfy>Nzp>d42V#X<)^3Ir'
    'iEr^;3#XGuvBy*sZ_56y_!%}p<y{M88+dT}l-^+02pSmT;46i22TlOluC3Pel-$)Ak!kFUmo8bKCqtUS9YH~~zE&?NkQDL;tbg`T'
    'by7_O>Rs(K#z{mgITWRr|atuzvns+oR!QwX25==vWk+Kf3extEb1P-#aj^E*SE&#rKPi140Z$8BV;C|Lwb>ZnBN@`L{<efR8_jVF'
    'H7bRmmMzS%LehDgnvJO*fTcI-^^nWlZd@cB#WVcl;PcYS&GPTIWt?=o${NS9k#q3QdHe=fF;q91eAnTYje><Uht7M`|JnA%o_j3T'
    'E40>WOV%@^Aogu_YIC;j1BW%b}%&fY*Do8oykfJd3AKi9x+JaMAw?1d&jM;_}Df;<cMspk%looo8-sixnd<bN#U@4-|i^(dxqvS0'
    'q{??_hatWh!9_H5k`PV*poTg3)Zb$mD%?Jqz4=s$e;4?o-Y@z4L$uYO@?P8Pq0n3dnelS}*%SsE1GY{N7({BplHYc(|d0P@gZ9&n'
    '6W`1znyhKNrEzjemLf2V>y@=i?N;78sdOISZrIW?Jgl!}?5v=w3Jh4S%Y)^OcI^;I)-WrxBWm64%#oGK>TkM>YDhQPWXr4A!cTY0'
    'WEYP-E&>J8WSwsJs@-zfX`L+m6$ntjB+esmAqmD%O3RdV|;q29V(g)aLS0)jEP>|e8Rn)du(855{J7`*%-BkPj3=;0eO+jMk~it-'
    '2921b-T(W#zivnjz}$}^7~6GO^eAI6J07dO#_t=~lDfnp;{V31`IUb-&q6}KyXC~e)j1o$q9f7S#hL$GLPI2)wRc5@EothjdA;zP'
    'l))t8}sfxxIlpt&joT$yAPD*2Z#K1a@mxl5E%xKw3*#rI@i#|ri7ct>M*Sor8%xKzG4FstGwS6Bvhi4muGiWv3K#v2jsRqIN~qrf'
    'Jksf?kfP0d!zwaDa>>bF%mzIa)zC;_VO`^r%Xabu|)QpA$vX|YJ-w$O-SHu2&(gbg-jTl~JcI#{E9gK^DXg6%MusB;3es)70mhbv'
    'F69S2y+*m8`RaXZ?`qhx=z<(57!sp)UonX3T;R>dV0AE<P>m*;w1r0?$fR`O$JOU$pqy;f&vf5YqPsd7E}-m^fr6IWmP<#Qwxh92'
    '3HbO0TDYOy4R>nF^|-Q^CpED(jC{lUqvT{wo=v?wo_a)@h7bD%aCkiV>&*&az6mc|FD6FB5WH?HyWjZ5C6ud-x*&{;$l=S#>4`NW'
    '|v-_s@M!(oL%?I4-NL)g!3H;B1TTMD7MAdfLX+T-=pryQBKnHL1A_jR$gfAjuIamwPhQCl9Uu=maB^;J&<SUqea9`w5UQN9E5Ij1'
    'I03d+&*D?-a)+NU1yK%79~;o@_Rf?cF7{8oI_!)y^|UyvP|i}TTs1BZNqyA0TOKJ)&XXGWBe#6IZWLXTj<>E}BiTMU~0A*%vAQX@'
    'tYR*f`FjV=t2iHej=iVL;_KZV=^vxD6YC@yCZJx{jp^cYa^_1pevI{^7dS|Y67nh)3UqZAzcBbXg~wTy=d?dZfCSZh}mIIb?wdXG'
    'Rx#~*5L9<3`ul_r_^_lUQ?6~Vl^A2w=#FNQQlY)HCGs{%x1Z>j@CSx~P$^V_x#Sd|1$+T+oRO>cF5HuWqL3nuNI?5(l8X$uAC8`O'
    '1$4u?ox=}GUY%>&~X$SpuwIr>n#YiiEH#0}&ryD)-7ja#nLvAjCrrZFh7B(r&Bx7kePO?)a3QX9PDcVT9Cfgne>%C%Bd01eqU+oE'
    'G|j07)}ojAH@>*A_h5-i-e(;?eU)`gRr-'
    'ZXkM-v5UCx|8#-nlzgqzbP*s;(zZ|wmA%*2<A%DlAP&3A;DW-T1jxiT>Bue;e}AWD>yx}acC--x+ETM_z@yCTxt<bN5r0{a4WW2d'
    ')hSlc5(u1<Yb<}u@YWS7fWwF{yr|Q(&9%F!eHe&h+r67_?5U+4q%%&Z_)D0u!AcRQL;+BvDRZn{9W0c?e3o3Zn4v-kSsRP#GHHl('
    'w@ijO)Sbhr}~NJ&q=yOc3KZ=2br`=w%YE-UwS6PiRPhL65rv`Gq1@)LHg%FCa76VK`H;_JI5&tQFfPxN%~jWX4@0U2lZb;1tTQ1{'
    'IKipHlsV*T3o+q|BbxzKmrcfW|<l`CpGy)QV1v7R#+S7d=EV-iNJUonRxwgViA>{kvRmg?9@nG63x$|*LHd1)ho6`&6j7n9GNwDR'
    '_ce7WZz+3zq4~=9n>U%V@Dr~?D;YTfB-u*w8f5N*)-h?+QJh~+pwb?+V-Q4*4n2to_4sqXJ#s=7EUEaP<{yYC<`Qm9hnFfN2T2uI'
    'tu|cnGDS8iEMI}_)rzQWcF8SB2B5`0$lT<dSKb2LN2vL581KLQ^W2kYOdJ1xUqDt!i#FVUcI37cH(hRp{OL=nQd+`r1R_XxKopaG'
    '@=pXbv%b}GyTAa_v*%0%S3uLb3}^whIG4BlRnj2vG^g2IZk%^+esWV3A7b+aUR$)6EbYeFA<Ogjh@I5TXBZ+uF(oJDBTGbICmRKG'
    '7PRQ-6{GBu_zI{8?n=A8RAkv=5qO^HvuR4S+a>dD$e!$f$FR#5e(Uv_SCSM@p+(-H=}bHM#n7sHiy!zf;q+ZiTCu0)cK*M@PM0eZ'
    'MKx`OxW;w-Oj(S&!V%Vp`oW?Taz8Ka^Zc!@{vhFo@E8w=ZNthajd}L4CJ?3sNqNgRQqntq2LX_dT86ifoC)BVp+}9EiU#hINo+!6'
    'WpIyHF=E^wu&c-^h;EILf(?B%N5wIQ!6DBFcPTPF34>*b8~gZJ|AhF0^<OxbFo(=GCMnax$=5TPMcrFrX*4ffV#RTqRYB@ZQiUKD'
    '%Y$my%r0q#uQ}10^5puaJH_l*|kL$ExIG@xK^}|=}A49wc|jfCIh`Fujf70i!ylkh#8sk<ijvC4vnSRwS9MIur#q;9GOUo5r;OG*'
    '6ghc?qWN8Yq;O9bmun6yr<?%*YlwU?#?ukKj^Cv1a9fEhKuFM_IK}jA(qr32Av(YrUQYfs3GI6Bc*n4r_?reIXKQiFj8N^OisMQj'
    'i-hyx3syu+@bzD6otZTtZv-MPZje|0$NQ@i6n*4#Qg%Y3fGohDQ~Xdi#_-n78YGxR}F?&7pVq#E4b~m%Lykt?x5gE0Judkf#Fqha'
    'oQ8|!l>lw0<!svk)6KWoX{YQ;LN8mmI#)$qf_>khjOl>bd)lrdbp|H^7@=ZbL;>jAV_4!D4s0O!){!COdhwfiXc>J?NVu}T~|=qB'
    '}6@nPO&~+Bc#<KMV*g<B`cr|>}aZrIweJ&Hb>CbqX~I22O1K3vx<2lQ<|O{bIe1krrk=3l1e#*GlSF@3on^BGqt&<lLcMoPPuLrI'
    '3B15b+vT;ZX`OW2$rfHP0Nn*8C&hW6<sEgvg=aJdJ3K>x)BhNbc|QHfh?{xn-$t@cSPT)?gYkvhvC4#`7`<=vxs$3M_$Q}%c%#&e'
    ')B<Z$K?g`6&d}xDRZ-NLR%<LPGpTE%CBjGrroLF;34ILy`e-sYGznglQUx55suWXo!32RQe0|5T7niJpq-eB5Brq?elL+PY^m#pL'
    'Yuf@0;0rj(OFQ0qFb2Bld`DV{ON9b3fkszwO8(oTo$K0r!C<HkMM%6!-BPh3)Z-s>PSM)rH@H>?U_LJ<o$DwV(S60Y3dTZHyRBrp'
    'n$~Ne6V8UVK_;(C(=oHNOa~W90@}R1-n5<%vWBXnf@!hK6?PQ)>SYBgmQeqw}hF`!gT;_u0F1y8BuPd-I35i6xh+r<r?4-DvnJ-*'
    'yHk?xn0UX-o8jPLhT(v3Q?H;8<FuPYMdi6Vv)tg=Kvl;jmu)y%ccH)Gd@79jjSgHK{(V2Sp#E4Mls5qMp%to<>@=+$rc$R3$o&=e'
    'WCF<%V_f+*3(MIW+pVx_H8UZvoQXTGF^eTD8lx<5kJ@)##_C3*)%Ev<hGJ0mrSp{1g#n!$NtgaY(zukW+dh@fPI<mw^?+-gdiGJy'
    '7qE&q??6YIQ@|~5trb)RfG#)gb)=4&Peo5f`B+YqPYT(1+NWF6C@7dXLQD7AAbm>FWcm@Gv<VTm7TxPc#d|d&p*g{MvLAG;C;pRQ'
    'CnF|ERt4|eK8^W6SNoyL1?$Fu()a?`o6bOPb`|#2#(_f&=>6?G>(;kP$7_cqm2SGr#0s{m#?=ACg3zyMD5`39{}}qvX#wlyJl^Ws'
    '*U2cHJY`qIFWw|8NFnG<8-Lv2xAwefTO^Ag?>NBj?xGyhlpO2ku@R>5?R*bR44lV#_yG0^;cx?@G$LpI~C!m&)yTA6@sA=rqk7w('
    'M}BkwYiFB4(A{wWQrprXl+$>jF+yknzY~Bd;*0LCHjPVP=j)@a~f?z)6Tmmw==3X1)ytZLEl#!B)-2iVev!M&_XfZ{?u70_N~-@S'
    ';2?+6xnJ7?18k`{<-P+SCHz&?Ulx{(Z7lSxrdMG6AgQ&kP2GdA#EUNVSUT~5n5|Vdau~WPhn2NR!jfZ_0dXK75i0uVc3Btv=14E7'
    '|eBun2bzM?B3;Dh<a(b>*|7Ne4XiwTp8vsC2v@VY~iuD$(Yq(uZy`v7QKu9I|?ZTkSi?*UNUJdebW@K;m{p@m~wd3$iY-Ic1%m$Q'
    'gSG^=K>9QH9D6~_IXdaB~lf*z4s1qBeV`zKsfdfV2WJ1B7(3PA}l%Mu1sMJsE|sC>hz(sly+TLV+d)&u$|3+wSy)!m}0O&;3anSs'
    '$E46W7<ESG_r~Pf=-0*0@?)6Bk2?k_F4NV3?ymc$D_&W(f^A~_-tCAT1d~)&GAQDHzQwUX!i#SJC%`l7bQ9FYAk$(&P^tc4KWo{M'
    '9r)F$`WZ+f=s*8w;L$8!--SX#S(k9!$~9vs&^tXwenX&SCS7+m-wu<xAyb8+TVa%10s<Mqu&zdd8%>WiPMcXkS|=<Ec(Iwe%E~NH'
    '(#7?{@wp5r02icSk5$kyYJMQ3r*)5Po$!1f2wTk4y@XCa;CH(h_M1Esq5baZ9|z>bWG%jl%-IMuqP+x+X5%Q1g4x3%0573c;(N4j'
    'v>Nof%dLwZ&Ti7@4)89RlYikl|xQ;rB9h^SRZmx?9R6>#~!KGVc~{!(MY800d8cwkP@T*QOSP<MMtM8O<O90dQYzsT#4eB3qT`bW'
    'f5+uhG8sxMzPKMRZcfIo;-EtOZ!sO*Jm$$i~pWG*Nj%R^WU7X1#sT}WosX>u9No9vrXr|K6M^#(AmkRGpCwP;y`m|S@I~vz3;RhH'
    'V8?2Z>+l4uh<w2%rlQPCq(T}({+9TLW@{<BMIC#l5Q@z81T#fCX&Q>NsQ{v7-4rV-QOH*ao~Vx6>fA!3f+*}u@Id=qixd0Y4$K-_'
    '?SgR#k4K?C6TrW-42wLw!bOVQcp#c$XjoR<~p@V;(Kz|kt>e#3*8SED{<W^Ye2aSn<zLW<{@>1wcDxp!;e~;cr?)ji%8rRBt|@#*'
    'Rtg`0W6-S2fuD+m7MPZR+uV@YGG1mbULaU1u~uy4~hYvc-5jDK}7>vB3`ZKm+gp}ki@}@ros#qF+2mC64xWpA&Alw>gEzK{M%T%R'
    '-qS{Ds%`N;1X&c0VJ5+8k-O=npEHQzU8NbSjQ$yZL5<HL&&|296K&v9*GrBnv~SJ#X*WnF|(c&UudMXb~*}~k3^I&T*x-)(vrFrN'
    'Eb6vUR_PKAA=G(idWm;cZCf)WsO$C&4skhl+3J|3tnSZ^loLF8G3+Z#9Z+TD|d}Ba!NNOQilp*3=7BVjdyYn_O7K<8#H~ZT{BEy('
    '5IDLZFI63qH>IV_mGTLgXQ`ystC9{2IX04)QYklGZMsVW2`B9lkC%9{E}K&<Au%17MqgNjnydYT6I}ZWMO=5#aToLnp{<OJTXok+'
    'mveD#kCU_sz5EuEhc?nphPY{++2MY68t+6k^B6ZPZk!LdG2n5wh4rx<<>SxMYb)vUDG#V`3V4aE@Oe<X!*G?$<e2(t03rnOyoAX^'
    '1})pZgZjI{sDXJbW_LGSEbB=Hgrfi`}b4c7T39;YDGM?<6xs&K!s`E_MHafh52jqQS8wvkkAU&!g+aEX*;YI#849W&*e(lE%!+O2'
    'Pb=Bz|o1pQox0`uwi7amL)}?50vE1rFdcsS1<*8d#tSmY~XNObJ13FshZ6$3eYAsB85k=e}RVr`bQeiO5f2v2zXWDz4*M?Ie{*kj'
    '`31km*kI8;JZmM0J;vufh)hE>j?lOt-Wb-_2?c|y0@O(6vEz9*37nU_I1jq^1!qEDLy8uyrGAs2P1H0$k^vO(Q#Bx80I~$g&$Npn'
    'l)j5C<umXPqQ|(u?przSP6@v_$Wj$QAZpFx-Q}GG*jZufEU7CyTj5g@*C();B_Q@@9!46bnE^Nv!ENxmy1(3koIFlcyv!3m@p$QK'
    ')#1bXhfupqpOy{o5>4FSQuIDHCH?G7rutPP`9wRDA)_>2#@ZmBF90EYIOlWH#9gld38bl$rvGl39Q_?4P)Vx!Y0ml9;ktNRCp6Qu'
    '#xPO9_WKH8n`b@a&%8bOWC67xS}R4{5Gl_JAnrs6fQ5Vp3SGPl$Qx;YL5`~coU29+02>Y0*%k25_xq|AYNAOtXeODue}Yab&HsuI'
    'tO&nwHn-N>GIz9^>*Yi0b*xc3Ax!KWovpPU<)p;%9aOV(aer_)LpnLF^zrW#gU0^NPi+N0<hWhHK;fU(Z6W_CNPxsr~bS0OLV9P^'
    '3&{lps+b4WebMx71trg_N%7TO^xT9vY$6!Xo4&M(N~RUzHB<({3Riv_Y`(PuXTOU))5fNQRzA?cOj=DddcjUHV7QnAYT_`_c+(KG'
    '1Xt>xH&#vP@yz$m?>c3AR}e_-+%ajRG!f6x1NotBW%*;`*!i5V4Ar)A(;yJYSFLFqA@;TGY)W{oqkD;$IkaC;*v7Q`^tKCP5RqG6'
    'zNs@K|U&*s$*0KkpSwd2?2CPl(gQQevHMWK-$%B;~{N|7-TsWlw2%c?<f|Aa{dF^j8(iQeB+X^w(bqgE=M9|QaNEKMTr|E6lo}8U'
    '9Z$VUut<MnqQnYj6^q9E^n^%I<#i=a>UE~n0SnqTi}X)_{)!@>lu_+vkE0!NUOSPtF3*Sjd*u;Ze)?8SO7>mOUmxzjfxzki-1x)o'
    'APtEX#5RLt=Xu`GY>@xf<cara4Xm=??qrReXTMx3eg6<a8w)^C_Pw>%Q`NpkTJyVY;mOd1z8p7b7&sK;Qj*h1HYWG6Xjk(hV%sJL'
    '1G%vz0G6-5*}ix@kKw>>g2{ekVuh$F~F~VxGv|&??r|tMZH*d><ujTTKq)UeP+EpmyeGi)YGnqkZ_%5Fn0mUyD5b~X`5YbVT2qkF'
    's2@ak<uIRCd*6cKvj3%-ImRTJ9egd8<w)l5pOLiES{i!(HzTy>cj;G>)`h-SKrMxAN0yp+T|&c{ZI_XFt8J5^KVfHoC?xTzaDN~G'
    '2(<8RG1)nRFnor)foxWFk%~ABSmb@{f~>2Pbw1Q8nDgqK@2D+EK?>E9ky=qlND6cb`sr&fIw`4+>i%22>BxUw00Z5&ehE#V`VwpN'
    'uK05G>5!*p!>-?!1CJ5;vD%Uls0rtvaU$y?jxouL-iRbXI=?zqSMgNvBT$jD(fp1Ty;8OI_l6LTt`n*<TaKbfKCv(uJ%{+xJZEKb'
    'l`R(P%{I+ajz`7F7@6f(H}rsa`l6!Rn%HC^Xf`%lHIHZ+@^Rw^($0QYc+<yyBo+SpmGX<?f<2sT7})J_Sx}mA226|JVL?uK?unY*'
    ')eSYm)<622=nB5^+k7}pVxi-rL#=rJ|4?{RW7D#AmlFShcm$0g-!u56@NB7Py7eUyvOv4c4Oo&2w-j0rfmn-yfQ!(Wyx2cIv=JDY'
    'Qh}A$aHT1%-_JzwGsls7er$^r`zoS*{{vN@odlByr9|<H)U_*O|k{4U#zPlx7xnQrE~e7r}_G3i+3(!KT%Q&5y9jO)o8GV`J+<xQ'
    '4<+wIJKCu)PpA%-J8@WOMBJOC2;v(ypfE#K+sy_#lls`J=vR)zSXUVF+PAM)j-~Ma+S-Wj<^EEZuM*m6r2y5<Ft|2*s&P8ZM6)@j'
    'sb3>vJ^*B(Wzu7tgZ~83E{oSE0{G(CJMVV5R*Fqd11n=sK43PPH31eK12C>ZAkba-#cuEkj%zkf_jEOLhlrjIjM70q})kHhp2K}p'
    'PQFnZn3_+!$yN`B3wb8H8xqc^VWtKYdL1f|7`vTD7>f=$n~a>O!7I1DyjBd1#9Q{9NbQP%pZv{M!ii)uvEH-{e9(y1vmqVW{-`_g'
    '=hXWfgVk}SAf|Mgp?)Uazzcr^-l8JOH;?dX93si5<0+R;-mrfs6Vgb6h3sk>8r-yo@zd~FA%TRFv-j7MXs`CrMxyMH?)#<QNag5K'
    'e<<hYj6xh&LxT~AmHRK*t)*%@y^5|LwT^%IWVQCy>$60vhbm$(XJq6YQ5%27@hj*D)Cf+x2pONS+rG1dDunHp4(Vj5MsdM8QcH4t'
    '>FPQx&#*Y=xeAQEY>fo&^E{=-oJt2aVc>w5@9%947;7VZf8EabDKQgBZR2mf+BNh?v+flXx*xKW|6KFS8ErxvQIM?BQP{qdMGPW5'
    't0IAG?CwUjYgFZH>e0k1*#pirv@%L9m(iJe{W>i+gQ3B`qo@{YR8VsC?ZlL;1S9Y+qzO5X1-Hj3;Z%VLgHP+jIg_9cNA`-AS;Ij0'
    'XChHopS?0x`{$r&~tM2kf}#N9Q2b!fCU^NY}w|pHM}OoF4M%o<y8VV(P|XdH<rAO!dqE`C=^B&TGwHz$l>S>GNHvrbDws@`=z`!t'
    'WSDUlnud<L`}2V^#W~a`6p)OvdV`m$X?Y;5dcgN1eOC_HWj5p2sV8UXoB$qk~j2fqZV&e6j75m)D(3EYCpkRa07Wy#+|ve9}s6V`'
    'vEg)xFo%mQmaxQSyc;WDiRBcaR5Esuhh!weU7?Rz+=88Z4gEhuIvqIFXnXFl(Ce7H(hsNFKz<4;d~jXN(D=hL7F&=AKP)Mr528~7'
    'K>)gf||$!B(qsrA>Ek;Pi?kb$_wMg7Gx{rthofFc?xs$<;8Jc#}`>@ETfRFbD)sp8JYoH-=&v%q!f7s*Svcu=m0Rm%Kge%+VAONH'
    '^BzSGy&j^1e2j@PPSsSW%J>4?2{)qH(t&8$1Gar3r?;e0p*Gz#{fv|rV_K*k}b9;%LLmUr-t=o7QZ<47)tEdR=qob=@|LYvHB5Io'
    '-0R0Y#(Nm2DE}9OTk;v`M6r@LVtR+Nzoda`3XC=A{3%yg1uy~Ic>^wE*pXmc6aMTq9awQpftB+--gGcZB;RD{Z%mh7<Q@pl5&$cy'
    '#+csAEZ*=4(204uk+nhn6c1%vC>!5OrrS84)p;0f%_HHmmY_&Uo71!M3L<P$WM9)K^$LY&--t9L+BEmES+jpf9w^Xy17^ZWzRXHY'
    '@Ay|)IAZ7vEU$nE`<d(eQ^O>?~hP@G)X=^Xh`2=fuLIlG@OPT%kb}|&dX2w(OSWT9R>~at+e5rRl15;RsfnSy`Q3{fC@-ln5ZTbb'
    'B-0In3{#;oxx_eDjvy^A7?QJ-z*@##l_rGd^HcFZ(-Jz;1Yy0y0oy03NlaBs*v6>ZarO5h8JTcouZg5)wZENi3Gkn*S{mlG1^=vA'
    '4)?6OO-6VxiHAKh6dS{h6Z<Z$ds8f_F_`Zo$k5eIyV8RmVMoP;x{aMZc{jcLqoO^h*8~db=gy9tz+#xGMNH8yLfMFXel;XxelIa2'
    'F1+S^{Du2BC-Q}SM<uuT0nf&-b`yfOd>zp9O+i7PWfM&osmcv6vb?nBaSC%!UcU$6rYQODHNFvp6qO;&Y-;&`RUO!PTghX%|rE9$'
    'TiO{4qtMv)S!wMwr~ud^n%j*SX4zO2?_O~UgpaS%iip=YzOjOX<*tG2=>M8HalRmzdiW3Kr*E+PTb{8c;bv&yPWEwRs==NJ>ya+_'
    'FGIo$3`ARQ(A8|QCng`4$-Q~*+pe!#m>&lZYJ_`PV@yr0f$qdnDoHag%pzLU{=)%#lyY_6;M$>&+G&b6M-cW9HG8Yw7Gc(mjMP(N'
    '`TgRaX+YpTgUp4fk_3Xu?e^*fInm!N<Z2divv$IO<UK7-3yFtu8g$BJS)U0SWa(`MqX7uw!4Lt`%a%NN>`y#bo?IBguNhCkKvID-'
    'wqX;?$Rm=H<xX4@cYtz?dw|PyYP%iWC=(B;Azhrf$*Wz1yn(aX^V?JqE@R7vKyzYT9PV9#<sg*tJTu6m+aM!Da<&q|Hn>np&5u>D'
    'vZo3F47L|GHzLEQIL1oCSw$uB?)&(14=V}2#U9rrUetB(2yHR3FAc(VI2dWz9WbMl<kbxBH!>X`@m1;*-Pg8p&DTRO84CWy{kP*0'
    '|h$^<+FBpc&O3{^zMah`}rA}hr>~>m5ik!N`B%P$~_m$7kf6A?*~|2dN6oFV{Fpa{`OUGE*5g=(^HC8J%p{^0g<D4x0UJTb5xF~q'
    'Q)Z!c`mkJ@axz>wUKSrlEW5B&O;Y<;Ts+C-Qm0|>aKAufpDVHqv(J-Ne$U_deMp$k2$Ku`orV)K%`dDl&~EHUUPghan>cM*VMUBJ'
    '$uz>k%|;PDv1Q-zC?Du*o&bft+KPpk>(rFRr}!XD9C1AsZ}C;sV@%xtsdK_(;i=jObtUUIMpd%Be7|2guUIF4`L7N#e%t3uipo)K'
    'HlA(D}ZHZ$1rmCVPG9C+*i0P>>KaPX`~yLg?|9N90C>Z47}3gNN15T&}+^@nn7l@w!TXEnVViSBmsfu1Exm^zlFFDQ9j^a(NpB_8'
    '8ITYsiaD(OUk|h=?>!UyP`}7+5CA1ArubjfvSMp#u@Y*gAicmWv!@h#ts@f@aX4BaUR0)fqP};Mg_-5{j><9hoq1CYyVIM6g3y2s'
    'D`G<5v;}J`tmw2%^dLzWZMBxk6BMvlE%8Y{H)m7BL9?bWSjRmNOMU|m=+48vI2nhJ$G7i^9K2Oq+1ek3=dEVh(q#f41jsH<!D@Jq'
    'MM$Ar)II;`it*7uGqcwZ>+H^cQy*Fy)TRv`(KuB%&LzGjHKqx%`Lga!_Wh7rG!m*Si1c(=FUhG^GV}>{q{eeI&%_DQED3wO%i9%H'
    'UAqD_l+kT&t7OccRqL8KKiwNlv(}P{k5JvK+um!6_42&REZz3oTU7so&u_T%gqiE#pQci0p<18QeoDK-TTIi4=zh`29D!vnnKbX_'
    'E(MP&NQ7rzwdn0g>TOOMM>{npPD)EIGuX(>_dIf;c>{wCPc40y#fapqjLgv9$sa$Yq>PjZ96{Z!qrZeGWu4{9I{Ta4~lah+EsZuT'
    'd|^N?thXsG=$p&oYfOE$E6!{>c4@@OJWU<^H(4KL+EShn*)HJS4VdK0h3AeA;_yfV*duH;5>G0PHYVuYa`goATI5(D`kX{Jrz_yA'
    'FT8x6d$aYXj#g4o@5`LZv5tCQ}$8w$tJu0FU~fdvETd-K&fxeoo_zJE?XIh#xI-BTp-m^&{-^X-_dmW-|Vn1^<AO6F;Z#5LMa9MN'
    '5E}?Uuf=Pd<r&9`_qA510yf4Kgo&P#G#wqz{NSzud$OAy;xmU;&<2t^5wwLqBiGZ=cLQ&XtM3h8o6z@3U)&%`Y?*EySK}$GqH}3j'
    '?5RgbNJ8FBU?qA*XAqf6l#mZMqFP9^JJtC65N5QRN;+b2JO;US&ou$^dzeZ5?h?Jkm{P!+RlUB)8$XVrK{fG;t2VzXD;#}51m5E-'
    'j3_aq8jAqMi2~>WnxxGOY9H`b4Af%*$MnA?vR={LmXEtv1)B@Dw3`Q+fK2f8vOxwib6IKoF0-x;|b^u;#i8^aF#5z?kw2+sR04;?'
    'n*B*7_4}D;HZY<&_<}}r>fHP=h&CuHl1m-e+Ppi$BpPElm{H;iUTde*pW9YT-#V#RWD@rkFs^7Rr)yv+YZ2KKykqicnO5`4`TE7w'
    '5L5<YC$Vro27~k%!w#OF)w0f`^V<y-^8XZPlV5OmG8EvO^z6eL{%hDE5}mQCph@+sO@OC{j=HrAW}AGaSq^IkUc*D33s4drX#mga'
    'nZi~r=JA)(rb&E$0DxMPMgX|MTvoB$Ky!}uOM+8>qI*Y=$ljf&NcaHc5D#Dx9me-H8uYB+Z4|JXAPvhy7HQV{FiS|o%l_B+AulEA'
    'gVv`=S>F{Mr&x2b{x=KHc;ZbuOVsr(+4PZ(?m^mw}te^K2v<LwwLrz9S?LxusbkUI-8VBCD1AR^dCRmcks|*bN%OSjI}y4XB$ua#'
    '*VokpZ)e2l&@@ek>T-{*@%XQ^576+u+i~`1_a;hW7wb*lhoT8hBdp{mu$rHm8Grw{m4+LDW!F&WF}M6(UVU@2WQ^4I{ko2Iw7rb-'
    'p=CHo%^lc(CMa=UpAdn^J^Ey18`S~t%%mXa$kpH*=b~lX#cD^yoA-@E{e|ug*f)<fB5*5PdL3&jaToFVBkWoqMJT#+O_kJ)=3YcN'
    'ftOCwVh-HA`a>41Oc(b)>?JGNW;meMrl3H%MS|l1(@XnILVy9TVPIg(5rR=yp@fJLqO@B2zoztgQNm@FT%2Me#`l`3L-)+3%o+A3'
    'Rh{WwfJIY6{Sv#4J$8f#242Jftq}6Bn-zm;iA+0R|$(dx0|iKW4krl90U3>Fir^(7XkjB+r1(F37}AHi|imwgKI=eIZ@3q&(S47D'
    'fj^R_R{msi5ux$dQ`~sL{IG{H%1KcZTo>*=u=050FXkAi>NzZUwGjFsYp52w0_w6Pnh5oh83L@S`!HgAgNB85xk7{d)cw3-#31J_'
    'OzY!$1-qT^|w*ZJ`Y(C--siQP`l}?1~s_rAd{6YLF)epp^ylb;)~L=xD;?8I|v<<Q|Pg#W_{^?n+1|pOfmuzMIw&0aa_ttSY*$}?'
    'dH;Xx7puX9gyoAprTR!e5o%6{diIArOhe)57ff$>Q9$1&lmf9z)rwSP_g}DL_tU?Op8)#FHwSxjcn^<#dg~<bL9mD-%^3O0>?&l3'
    'Mx-j@o_liksd~EHCzZaWy^S6IXI3(-YiVmAlxN-^_T$`RzqAeRUlWSZmZ+BYF{?c{V0aQCJcV9w<9wocB<#qUaI-LE&x3iZJFc3c'
    '=Jj`=@q$YBE_(@)GdK|Bjrh;EYs5Up1vzRl9?@Czg3=@cIHdFLpY)I9`pw6+Du@P$fx4U`h4-%-ejj~8NQ@-oH~3}tTbk~ZPqG{s'
    'pZ-GB(!|(5%YQ7YyVtNrJGT;2<dsuroz~F*mAwftTdfzm!)+QwBp@45soMwE^%Bh_o~P<`dG<`h-GzJB1T`RG>$&D^%k%xe@^z{w'
    'a+De_+YxW?T$F9Jg4J~;*1%2sWS|qFfJdmN?z7{I2k|($dNXmTxREEWqRTb<?u%%8~XHER8J->!ac?8>caC9hnZ|KYr&cXgh-|Hi'
    'Nm5crQ%B;UW85gohPrEbuyJaD;CC!uPQ^Wc@>pR;?qg_K;%Gf^B7CBx2yZ4`9zJIZjde@#BXd1NIX7Qxxfa}^Knli={EByp3!L0a'
    'H<#Yi!Z&*i(x^xliwta>Q1f`QS5K$I$;mW#}Ey~&DlV#&AeJN^q%crmU;59-k}GUZa&^;(Bl$&%62cz%~P^u@M!!8yN&>60<{6b1'
    'C^(<_f?*`W6un$XjYtaAw57`alF9deQTOCOdT)JR1Cq3E{)&Yy4kK`MqI9#y~&6EBGsc2HhmBsZYy8DmoA>F3BogRWGaE{a(Y$<+'
    'Xa$NIS+gCwgG4kL=6>)ckmxwNzM=BuJzL8b{u4kv5{fD2{HRnD^96~s=XDP$zyMdggsK9>|xg-?RxKZJB?A}VBIZ;w9o3yj*v8T?'
    'i?YS%1`geGepoIkBh)YQ8^nDetBOZoRkQ=lS>k*gLqJ>t2$m4WF50BQFYHTRC}{+H$i*T0qB$<Dh}4B)OTg;)>LWeLH7ON#P@@fw'
    'aAp1Sg5(#v7Ng!j`_U~<6fMO0hAD8{n#8QApTrftM%-;Q(re_LdaXVxWhAv(IGJ?F08a4jZz0w<r={p11O?ZRl%*6Hj;#b-6A4hc'
    'eoALcan0+4!(;fuOk8a+FNXYDz3BQ$jIi(LagD7#Zjej8)GgaLURIBZok*Y(uxbMXE91~As#%`yA)IqLPYyk{4KUWE0D<AYs#fHi'
    'wK<}_`H=bx;zaoeF&HK%IE5ex&7{9xI$&CHxw=P#cF(t3$o*J`;F5#qk3YHJD^ew?s~X#S2c+T0kqcbHT*yI2Jd0ly>}=Tq$w?|x'
    '+k1d>t2PfD<G^;WEy&)YS8vORv)vrk!Z_xyZ5lksedzl)83r#$mlb&@f3a{p}4oRU;N^i!mpHDuf-nK8e8yH^Z5(8)L9%!9R_sWJ'
    'h)rBJrE0fF}GH+Yi~@b{m(IMi7l(~S(NXpnOHYUStx)9e)!t=5g$0+{|B%=4<2Ck`6B2?0_UiA4AF#<Y-l**tngQ%u!xEfqp(&4+'
    'Nqv6gy1blaPS=%i2dGxjn<=DRybw1B)3iIA-TDTeyc*95Lpr*phgPhZHDp=8&cnmz2GkCO@JP(B6U(2XgOGqi!yJ=w1fCMdNs-&4'
    '2$AKRd)dB!4(9;@q2Ug&2|(E_r>c7!sK_~J^TCIk+Z+g-2VvrRy*#rims5EfY2nTz{trE0>b&7_lU+vIHn7W&cvS2eqXhPwlB4?c'
    'a#pU+B>`FfdA+28yuO3<95ITB39Iom+J7`exp`lX;Psm_|1jWW{YzF(A@mx=_b#=?6`n51y#H02(CpPIC0<v%;1hor?PGa`rc;U_'
    '<lKrdmI*81<Ah1i%2v;Wxs?yS-RZ{RWSRpd+05n;~<{n9hejmC=xUq5w}8sW$fIBZ1E-9i<MW{nB1WbWr#+ha2*|WB#qua0D1Vko'
    'J-t{<<{ZNOZS}+g#8%1S&EvR$Q#cj-Q(~MyI~4?DS^ME#s%aS`1r%DviRS!-ta$v8$c+sxcuB~f$C<?Fch5DfBx#*+&?z|Zu>kPZ'
    '~o@Y$;NZv=7M+X7d;f#w;wf;iZf7p!zm3Oo{Mz4kGAa|HiJ9(qlbeD)_J6tT^cQjQXk7X;V`8?>V&0H-9o38T5+qmMoH=xTO;0PH'
    '1A+E?@HT^P3x0SHw$;zO9ibvKwp_S-pM&dKXa8+N>+r!VNdB0uv?~Vt~>9JoQ+MFI1>EY(ybW~HW5G`vx3{dUH$eBVF?{N=ub0QL'
    '1c?-o0~ZAPCtpU0aTRVK_sDCL*8=g4#BZFbSNs2iHDX44H%nqB3{mk=QAflWYHSG?G4~V9-{c%{np4i9L<w63J1!7@@(&J#?_~ny'
    'IQX%4RqgFsrz4Zw%K$hIWs`3Q^9ZB*LaBngoGBK-R*9Ac*zy)0@O29FyHh=d2ol+MO5nIu57uo)j5c?%;dES7P|Xpv3~+TQpYH?u'
    '(Te0|478l*0^t>bi&O$`O;CT{pdJU*~ET>yC2FR?LEKp7NdO!>*ie;#^$q4XEOO<jHsu;y4P_Fz&D=Hn^fdQN)h?=IsHa*Ft(h2Q'
    '2W&kolYtwYg6X5AGeRhYpcx&g%qi0>|gi2eJGfRihRc6Gh~2fZc8|$2jijY!0R`<C|#1+Cnc}KB4tWy8zT2uNaJ__WDeb_^Q^0V-'
    'F&j?G;<Pkl`1(FU!Y!6*jDHEhW(RGHqRK{sV4T1kGvsm0zfD+D!H`k5vnrae6-MatTWvg#~RQ7X5Z=NFHfD>*Z9qaubR)D`mc>nU'
    '93SLc=s<IbWOUsIMZcYFrI(5Bpt@m<3u2}BmCXDQx{I1`EuWh=C9Avq`*n|=J>f&C*mLv9r#NJxly==iX1h@ssDgJA!Ue}mgoTvI'
    'xP4vntrzrmjefsy^;27w!Su2rfLU{!qB<KFZZ2oK7R^xY7fB?3sRt}q72i)Luth7eW(de4QEvX_JaWi7ls6?Qoqdpa_~K-I#$569'
    'uzkVa?jE-kgiwBH>ozU_{_pS(OcTea-<UR&njQa=e1=dtn&9Qqp}i-U+e4|^lnuu7xjxWI?`-)l2FE0qW0q_RaQ*0L-8)k1#RyX('
    '+y&`my^n&CWiOt2>g6U@l{LJ)s+Q`S4S2|1Gl3!$puvgr47W-D{9Ee5+hk2jIrnUJFcjVXa7(%9}RPAZfk5Had4HQORQ~G6?B1fi'
    'zx1uKGob6;IN8P@M^`<lDc7%LSA*bT#?z%HFe0^9XW`3s;aIpJ+3LLd+mTC3B~+eT32_Gz`}FX+XGle#dQa}tvnU=gcFh_<y%6O!'
    '8hj0rp%?*MCNG)(`hmvPHwEM$7-g}ELu>&Vjle1I2--5#k+GB<*0__l(J5O#Xq3Cr@9td!2<;(kT3&Lt)STc(#JJ+d-G8tmK(M%u'
    '986ZlA2~p3uslYBDI&<VvRUd@36f=+o=i__n&ws)$W~eOxPL-BUWLnrWU6v+{W^FWW6HyJ?baEDA+TCz6n`jmqW*Ayz;?#flP1L('
    'W$QbnlxtlCwoFPCzHNhm~*^kHW46leKSakW>wQE=5L@pImb=}KD$!t>D;<PRRm28r{pgx;lBPz*!@)8^Zp(?_m7@A)A)7M``Ip^J'
    '8BowGusdEXHun8RZOQLHsR_Z-FRYF(P&Amth|p>*IGcdN`^FFo_S;|%#YIiji_lHfxYG^!dE028G*)+u@R_NDq!i+Gha7duw&vv<'
    'NMjsGg)@Ve3Sot;oGzJ6kj)e-F)s_{8B%E(bRPExV<6##-7O@?^B(-AD?_bJO0iWhY!DV{GEO89NB+--@8ZNd1qha!S@>Xy|e%1{'
    'zE64jx?P-`L3BQ%#m%X)vFY~f9!CuWD0;a5(7e*0aXs;1ZDr+blf>0VO{Ov7M6>5ZbcH!&FL<bbID#IK6XtZMC8~nS52#=scdL)_'
    'M{+siHP_`fB8Y)eABH?C+ZZIEL9T4VVWN-cDOoesr#z!vYc9~qP$VyJVUl|!J_=d96&d-9h(!a<vUL{L27I}D1B495&HZuuj^&$;'
    'M{!PU3_&3(7e8tNaAX-$@>ir-f@&2+<)-!zWs*-SshPQ@U8CgqhGxmqYrDA9XcFZZpCG059JcCbMvx*5@kkd8=N)6WTs|v)oIo^$'
    'M!WXYVQlkmFGK(J?%LVFy_bO8$~~<m{0A!f#o7R^R;ChE#vpfc4a;`mcHJ{Qcr1g#IEm}@aUz!i>Ko#!G1y3yJ~(?pCBY;E;MJ2X'
    'V0GIOsP5^N1&kx<oH5qsr~4lbaq6uVVg40uoNmJ34y;Ai=d*K&i0o@r?wA!Gwj)sK%WVge<Uiy;=y`ZG5=ynu5ht%-3|k>fx?XA('
    'E$bzu&1l6*JBaoDho?jB{H!!f{y%#7W{AsIT*|E-qjgrD_jQzqUEr7OV1zLl5>GHKE3TtG&r<ADbCO9m1e(fK7FF;jG2O_tT;Xn6'
    'rlwhI@=NtzI6G1DgPLlR5I(^3(p#lH-B^C{o|(_&-~`!9Y5WC;x|nvWw)GlY<V0+CF|`J0eKwh&9uC*sYLI^&_K-5$hLp>8Nz|43'
    '#U-Dt*)bg{`vpFgW-*#UiJC#`j6Kf6HaS)V=jCx=ZuR4OeoEc5*2Vlm?GN@bjY<Y`BAuW?1{0M(3{Rk)S9yZ@z>{@&Sjrk6#Z>x)'
    '(9L+7EC}>y32gtty^dcH-{AgLZL${ao``~UFEa=wN8*P_8)ln`2MDY`wso&&`<WgyMO=TeMb(xV`oXzfyU$S9cViF&WQu@1kt1jO'
    '=?qvbHQgP%Yhjdc%U##H2yoxO5JCly|zI4`R6EU+4cCjor^K{8H-8kiHNSe2y|?XkOKoNm4C=;ralbUrnmYfd{uFIHOz*FolSk6o'
    'BDnGkJQWBo0(A150Z+~?U!KA>Fka5^M(dj)1@b^D5RmGPNRw)VWcYV>@80|%&EkoGEXu>eDvd=|DQkm^grc3{pH7h_p98mKK|%ux'
    'qtrjpZ*>c5ah34g~HdRVbEFLeo4{WL2`xNa|n$c&4KqZYQO>iSM!EItI^-9WliS)w0oWnH0F#WjcJBv=B5AQmZQWOkY#%_6;JGM-'
    'T}%giv1pzD(U*9+14q5B_BqpfP)&ovx*~e{N7vD$iXA@{Q9mZO|ogT;=Lg(Ks}gB{B`0IL2NjBS<es$>lC~q^9qIH^zrR8B9Tzr{'
    '9e_L)r*?&w<Pr(Jvk<#`pI#nq@-2PE#6^kAZJS(S^3GkJCfcB&s@WD*U(_*0o$!lOR+TY6l9l$ezb6m-K$eGo4h~$0nh$o5!tjrt'
    '8B&}U4e17i1V5*KWq_-D@gM6lzQ4KkRdD|L^FIaIMPp_em{7}X8p6zUI(xG{Bxo5z$ZC9<s6bLpzqVND?x@}3YfZ)iDGO2=KYmAb'
    'zPQkT+%eaap~^1r`6*WCvO)=Zr8)JlLJ6#a$F+5L6K4^^ktPToGBebBL0s)`S90&_cJ>+GPs_$+oJ8LG1wT{%nRjO5sOnHF~&&te'
    '8`ZU|M9<_Z$5)dslwu)dT#M>4<LZ%4Ln$}TT8bVjxT*gt+-r;B#lN@{i)>_bG62FtL-kTn)in4fG-^{fI)KRA_o7a`R9w9>O?d>`'
    'Y=28-*P`bA?NWwhY#Y&_~z_BOxFIx`;X*~X`K<kSFu|_`<lzpyJQiwnKk%Q)z{<4l!$=Z%+N$UP&p%8kLJx=!Q!8~1b*J3XBL}dG'
    'Z5MQddH&GjtF}#`_ALF?C5t;ZXpzzvfrA04^<}ds#dJ|PqK93)Jqep<5nCjw0OWPwsa3XOy-;2UvUmnXj!cN&@vDczOYbCxGO_J6'
    '!##a`0)XWRp#tp5%cW=-{c0Y0}HvUDz_@BM#aTm|IO%Am$Y<pomOm$X^LMar7b&`W^Fw2QF(V#My*}8p{9snk634xN{fqTDk{fgT'
    'gY`fB^rYn+#%!?b9xkTyAcUp92olJt8uU+<0lwI!kKXL#NRwvu_X*2cLc5e_IBT*;A}_zX7?;fy@zeXXCI<zIftHOYHb`YPCc+m+'
    'j~%@H@iQlFR~d`Cei-Qh2Vk<led%S#c~Ul1JL(QiyK9HZ|+xIKS=q-)`dMv453uScU@N;8Hu{Cg1+o^sdz4ddWnZ2e9@~uY-8qFb'
    '8(FIm?0b0XP<>^_3jgtEO5`2c0E#2K`WaJIGlp$rnVicvY=U%m6zV}Zl@y+#cRuhpMQ?@0Qpsge#n`Jv30j^b6_E-QW;LsrTh9wP'
    'J|rqt4#KNSN`DpN7Ut(j1(63z8d4_G=Cu%05`W23GwWc1G!HQW`TLC6%59HSMi%5%zD)#^oY4BNI;C}TDJa!u#8lSQ=OKTTgpenu'
    'L<oyV48STP~9>6Z@K7ho@hLGa^Hofug{)tywLPxOvwMhXy&!0mFh@l_m9>@G4i93S)3li=}i82t+zU4UWpH>CmlKx-T<f`Z~CJ7T'
    '$63r#xICTO=>abzNz?<>1K6XEdzG6{qn<4Kh6F9haZ1ZZyyNd7|6r{akMx4@Z)m(wXKywnK8Vic+zn#)L~g_Rp#W-<ZO%B(CW>r*'
    'nD~o3}ow_(suf5MLmrN-aT~q#PLJ>-aT>Tz`l3iX?ky8<DvHs?R&5B-NvTI!zYgX<m8bU_53uml}j2Ln=I}tML?QdQf3uwP0{RQq'
    'HVV%;Y7OY=(NaXYt=K_&XDhfa_z-cd(iUwE!&oQQ$G-{3`W|w*`?W*g*j`NkNnWmQ)(Hr=tl~k-*-(26(2g9ddZiS*Dd%<iad7ov'
    'fPGNHM`xJ#&hB)-`Di}rW4=rU+0_7HtLst`kue=?dhg{wqExArqPzV|F-G(wqH)!?rFBgfA+#Rji=L(kmIK?;E9E{yT#54KvJLwV'
    '!lQ8AMhzuy6w=Bde`XF@b`4uHPz0Hk2hpdnoWE-&kr1_I?rvICAMTiYggVYNOqbe?<vYB<kHE@4!nQB=Cy!BY`Saf`Z_oyXxaYwY'
    'V2H-J?OVtDL*I`08~&&_fL1`F(~S$9+$4e4VBG7c59ejB~^3pPZ0V5(}#dM!PCi_hZue88<1K#Gn3HV^zADzy`e!jK<X$zQS2Ckb'
    'mVMr&JJ)Li#gOiW_u~i+ND>)z}YUwFTf}=0(S64AOcevDklx<1jaB0cF+0SHjtUy<ppjMq<&F}y;qT)K)h~7AU{#+<C9=;cEH+9Z'
    'Y-H+adl&9n5-^1EL{J2UP@gR=)(0|x&{8t;vJsb{&)2X%@inPAt=-OC+(O_xD4VYDupSZ((e{i8^{*&{AT+D^(cdLvT)!2L`4%yy'
    'T%z0k4SAdZ%{5>>bK>^(5jc2x8WZY&n+z>{O9^m>V50UO@L@Y0@Oh_5v;;CJ%Jpg*G#m1SHF+HH1mpIjy^QM2=KA%a}uO7*TWN%q'
    '@Uhc>P8UKvSu#JR!f(FEtPBXi;)Q|{x#5MnorGKLtIXLQjkba59LsioF2$|zs2Os(#Se!r)HMZC&uWO+d(hc+gZAvCrhi}9QEUsf'
    '|*IEO0D!s)WJeFyrZov<HbH`8*jErQRi~@PPX~r3gscgvrCtDEni%J5-)mi|NgJfXQ0J$(b(R=$Z79upjTU=bh*R+rk%kjk~__Dy'
    'OeECU~=SWzHhm2uO){BG+mI&vspYJIAu`GEp#aNRGD7>2+z{ndc(+hZG3)q+4IdoBCEW<T3&h_ub>~(&r~kbKggyHVrr>j=apE1C'
    'BhW*_ZzDdVIHX4i#((~I~vVaf)Fs@2M(;wYxAzBGTUFe<@HHPEyc_3_JMHZ`3Obl>6$M#v3H(`pS0#W`ehK6?G4<cW>peTR9kHPG'
    '6@l<w}uwr<nZrUTeeOJleVoK-;N7S7CKN$Mu7nVKHepPzooHz&>m9~r*#;Zzu7UEL+5_=5!#~J^&@Ra6_S9+n?u7*%;(i-0H+M!!'
    'g$T3P2VhW$J;={v$~`etRRh$qhP2H;kD?({iA0@m=@gFR*6W4&)#C*(QfN>k+iF~q%Lb#*B*nrbg!eyr|+pph>27%Q&H=yxRmE-R'
    'TSB)Q%dR4J$@sLgL=RS6-qHRqtpSDUa&m#497W1%_(SZ&Xu|BH)l82X3A^xBq;T9@}nP}YbMR3ooyhn!zVXe*S3a1NsARQ_zJ8!J'
    '2)g+Wf1wX;=ScKRDQ85_6H_jtw=nOvWbqZjR*fhn$sZ%#F^|Bc`C1S?AWoRXFf~U=I2>leNIKMlotU-`k+P3b?Jx77klh=<jmhF-'
    'hN_BO-{9r5D|^yHO;)h4qbj`u{Rm>hIkoXusqa{-0a+1__B>jhtcQkNw&LmJ)sK`T5ox6)Y5n`5o&WY98b{yrD<);j_%PRl`Hllm'
    '7XizZY{RI%*E3rhYZ&E0L0Sl1sCq~SEBo#T{yZY`{{lg5oLNTOrPKa|4e6xy&}eYwrbX&<Vx*#a(4XWigSZG9TK_H&9(C2P|kLTo'
    'fHi8Ks>m1(RM{VSimAO)M`sJKA27;kdW{^vWL;{ePR&t1M^J%7_w^O*zUjQBjXvM)YRu0Waz4lKg)ePodNcs@iZ~N)_f$pubw(RT'
    'ri=swsyxlyZoIeIkOjbH4BZ7?y0Z&YBb3jobS1wWeW{#^7x7z0z%+8oKHJI+8=D&p75uK#WcCGasxg|XIq_wN#GjV$m-$ag%5!L!'
    '#)7_i=U|v_aC;2_&keKjk?tf^UT`Wtb17=4F1&JUFv5?@(rYs@d^-m7K+^yTek+V={f6|jd(4D;S0AnFHW1=Vlz`(Z-GfF6l}}nK'
    '!P%bL`H6F>``%Wm1!6YUdR2NB0WXDERM4@`o!Lu++}DOj<!Ha=i0J_%pm3QLH3{R|M}LX9r*j|;1x~{|6vOb-m5E2{EMWI7sj!)Y'
    ')orIwqj-DoS08o99J;u=`8jRfqDDIWm<}T8IB?Iz7iiO$SC%sg0{^c0(&SWqLug%b3Bx$-pPtnkANPsl|{b@XvV2WTSL>EeK<7zX'
    '7EU^z1$k<ka%zEnv@pjfn-Ml5l%_qv8(k|K$+CF9Q_r)?&eBAwO6l!f3oEwTG&{W2VcvmMK^h!16!S)a8;<7@nO6+C}zX2+H_!=1'
    '=Im(#~S@=J9lDj2>Y|Mg@W@8U0&Hc1lCJ%Gm8jHm&Rk3$DD^+{tSUbV;%w}Dgi~|9N0*2A@g?r+w&KizP9D~#B3;dcLsU^SMKZLo'
    'BM-b1laQR85F~6(Z^=Q%p$K|-B{^1Yqr%g4tH$cp&lD6ckS`)BOJPnJfO1qiHJ2qzb*}V=9k;Lf5Sp``x+o^PbXm_cn;tnEw;IPY'
    '-4RKU$%?3KPk5XQVpk^-hj+kajsveMgWy~kF8sIi>1O1Kx4l)B-DXo(_GLK#$MI0B-P1rViScA2W|wHh=u1j^8(Ko_Nl5=_!ktKU'
    'Lo$um0w)8#m_5();T}mM%cbY{Rrk7Z)qdjtcP*h)7o=J_Ac35*<Y~%Ycbi@<oOrJi@eI%9U)BY07i^!Yq15%5M8?I>*jwum9xcD>'
    '|cgt$VA8H1T{y<#hcq$x>s(yC_f3^cOx$a&`lQ4@G;TU!nh`{7MW?D=YWpB-8|>2-zgMDCZB-OX|`7v;mrE9`keZNrZy2spGSpwI'
    'M#tRc)2pG`q8GR9@}SVLaw;&UOCS;dzv2>x!UxS*-TTj6WfmcHBzB(*K}5$|9p$@)FeV#m%NL7x2Jq8s!ufO>puRlKBvAfR6B1w2'
    '!LW8z|J`R(eixG)5`fPxrz_f{uFH?61Tt7qpW{U*D3dpq8u^%s+@le*@x8`XizIzVLr=xOKCxUR>(K)RYQ|j@?J*>u#EzqCLmw~e'
    'a^K!vi8?Xg&t~be0Tp(nhxzdaq_+Q_Pu-Zz5V-+A82aacl^-dFHU@M=-|62oA$?4s6_#_6NxCNW+nj!N#-&wSSqe&hVcz35S90KM'
    '}Y9}udi`>&magLL<&rp$X4r%;@V>y!0YiqsCpnuNQ%Wca-H=Trymh>rNY2or>9vPLE_;$j!$I7Y~k?nsr`Dh*blHeQ1-~VA=am3d'
    'J`l7e`3AopTGK6<w?$Q&exmPn6KWOv*s>u-5Hdtz+;gbl}{&y35g(zSoWe!I;qFSXrr2xxv?w}@ZQx(3k^Y(H|nC48!l~nzU=Y~N'
    '~l)%9HyB5gn(b9_y1cKxR~?{yG4MFE>Nm>MEem*LEn+K*gVradRZ>5{iY(Cn7?8_?z&aY6__5v=e>Oc%>l|f=b=cL?&0qiHN)R6#'
    '(fO8iCa0+c=CaqBZ@*w_5i>BINbmr2nL^1V_2#0!N$r<wTH3srNUqx_SXmACE*P0H_G{5!kpqPe~@m!?L%UuZtnnrK>uv9f5P1FB'
    '*9SKM-GoyWNUK;Mos4RXr{}n3%0uL?KU(ZzluKDT)DNUym0sEo`!}x03M|faCNP`XopEpscWY6>Y8Hw`skSX1$4MdT05t=fXg7^t'
    'eWZ6ye+@d#By<D;sby@pm2=PUw^Lq&>rt^?f-vd7v37~SN8>gA=d%qFD^W)&wzpnN>>`MEx}^vARd<u2<6#s3rqlLoozva?*S0O)'
    '%Q8N2e_Tk>fqhMwkP;Um=%0S7c4C<S`?s<@N4#@`pF`{Dow>)rJI;dFsm!rNWIr_Fqq>u*DvM^SbQBLcmXSyQa&3EK<y4VkS$(Ra'
    '@1Zh^%Duo62jM8m&huiyh?W9za8}UJoADpC(B&FBy-7q*vX{l;zJu%P8+}`frIJ%Aq)WF!Sn<3NTNPts)~p>KGNT~up)*9l<mEts'
    'uOQ-Y=73UG}@Fb93Kv6a^61eB6Y9&xkUFfCrwzF%n}9MBI`#(gM%#S(AyhZo|mtwH3#wwtds7C?D}l;JidqmbiMw^TSGHaeKVs5F'
    'gqirVt-ei-d0XzrRC1Y7ROlRk_&)#_>IDLJ+VumC572^fdCjPhn%`v!+F1khVuGtHH_OoU(5l2zzAQ8S7rd~kdEnpj3Ty-pJJ8aO'
    'ZL7{hAnknaWg`Rl)A0<UhLT7s2QItIoob10!gl?K1qeIpa{+()EI_R*P6`;d)B3(k_+4-FiS81OG_fB$GLL-E=Dpu03juwjTn-KD'
    '9M;g)>*IGpE@oByP$UuS&-b<2HfC453CvuWWAc2&@xBMN_*(?6I?$SoCtRI{`yy+Fr8VEElw1D>LizE`@TwyxE{Nn)KE%VK^BJy-'
    'AWP~ITyNVUXerM1@vY!!<!G+>&zH{$8olIqkuTqCK3~1-(o|**vZ1z@v^ElZ}x6ey*nIcik_cOmKhi=5w%ny8`vi<zRH#+0#Oh%+'
    'X{71iesh7p75Gd*mavL>q=u{R*@r|f_Pcx=Engan-5ktR(o?I3(YA@7$mQ-SXb?`xsVywp^d#2`4lWfv**Os$zGteHVy(M9O~?)9'
    'NqITNQ`4s%6dZbfoBLqJX|RG{w-aiKD7~5ZU(~0#M6i^4O)ejYMrBllf|P42u;elR3F|>>C(tLlH)e<#K$9rCHZ#oIoWc{f{Iium'
    'hnw{gBe#qxP=&bWeaeesB`n!#;OaJT2NV@Y%%`?of8%ec2A;>ItTqveLzeH$D9elMVuz@!pQ3b-z}D|z1$q>Hm`+IvK<u`H)|1?N'
    '5c+Q?5+epRtI%=reyyGK&f6lS7IY7i!02T)?fAV1erl$HsB#fKrhsFMawmaHgUb`yV%bOWOgrJp)E^F6YiBhlsv1+dqi8`)%l|)o'
    '^WI%^v7(}0mmDTYJ|Ao4^G9o*7+%=#@r5?$2C~0qi1AyQxM0W37WDu5%5a_5ZEuafu~Sw_5nyzAmXF2=-v*Zq_Cyxd2H0enjJkOO'
    'W++l+(Yn>6Ec#$ve4Qmh~(nYoW!G><Bzs(zNmwBt*3wO_K!=o62sQH?k=k9cNrE2!O6Iae*JM=ICXI?@tUtOityMI#jZM?saWF(8'
    'oqOS;`+U*h-Y0Y;h6)Ryl`Jfq`HR_Q}X@r#2=UXcgBq>SBR}=5j^)ivL=Mw(Wn5N5y9tg2Mq-xGC)HKR*NW5OhYval#^YB0>!NEg'
    'aSRf2i>l(MS`Zh$ijod5Wi(OTE7c+)M0!B_aT_kdq<B|w|VV_MaH>e&&uYFq1?xR_p^HQbx;<&^>}Oi`CH&k|16+s3hxEk>09GRN'
    '$~P!{Ag+HUJ4zJp!52GA!%PCZ>@}T{^JibMU>80K6#h?0k&!^#F6`tX84s$b*0;_b_ke(#tK@_E;OR~vfo2}#Wp;j@zgz!?JDNUO'
    'vBbM<1zy{slmtvf^qXvEha1V-Sft6H9TLN$5rmgq0c`4u+EMY`+g4?Q}9dk4e0de{KG(^ag*7t1z7uT5kUXe;p|NFxeH%q=Pxv#!'
    '>=a9xx+VFS&X&_LFc6|@yRE)4(pso-<JG{xem6E&HCi$Byv|D_;s<a0Tr&`k}GW3#i>q+?vh*<ajTn2vc3C>8lzX(n)dwWqs8LfY'
    'I*ja7A4%i*{zWd7W3?1ln}KsBAE`BABU)oO++exIjbo6bQZiNt8;pDWWfS@pix@^W{93RfNcyk{cR`6;12{ARu4F;FwZ9C|B_V5o'
    'L{XiyU6>hx#VP<b9Os(E$XxH7UhHL)53fd`(OgzSF?Anv^25RdNn8HhkrpVML_8j%_wX6zN+sz=?vz6*K{1X{O}F#3j0Rs*^2pTI'
    'aj>1ze%AkSDw9O|EVYYbc>{3?I}wOw?EK?=!uHG-DZ+e6|~5jqejakM`N3hjfC86!3xGCRkVa3q@rIoJJqz-aaAOMYZz+(2LV=&K'
    'J8-a-cHu7mWfEWB>xj_%I)_c@P%R{YbfOxzy;>eV`3xZUs!S+k-V;FcP*-!TtN=PR%%1T_g(i6f_p=YCCI5(d_<eot~J!^NauuQL'
    '|duKM4XFw9LNHp;>9O;<3#-B8TmKxB3Gy4U-#i{VB|`6OId+R4mlqihrU}(nU96rk@~!j{9Kv|OLOQ_MMxV0Kd4T<A!b%1P<<K@#'
    ';l4yi{OJQr#fEiP#~dPYd!nJT$>fw0&1lFB7XfkUj=@>l0N21nM=7Bp=AC^NC3Tc65pi`{_-ZypJpnd{m{~5GrrSX`Nv>wVa9;zt'
    'h|q~gt5D>^z7J<G{x*ri}dLIThw-h({%Z%9nrc7B{k-=|M;{2_={itE84C$7w!~aT~~GqTyDTy&u6>Qbi+#h`21JL*i+ShPnfpQ0'
    'P5x3*wTdocd_FM;if&(ZVSrm6RaUKQA<n}wPEZqy54S9(yv6$YUOu6h-^Xs^p4HyhaY46BXt5oS39G-EZ$1BW?R^O5v2(C!Ip1Zy'
    'x9l4&MpV}S8JnH19<GR#Jsq6t_#Z7bkf>2tNP)z7#5$Y-kSEn)^sonmHT#8m%wkNeoRvDu?{VgfvxN7n<GmR9zOFzm1H}(llNMw0'
    'n7{w`I3JEr2hIXXAut#BHLP8JuLRd)=J-I5^|_(7IC)vC35PP(3(+169;FXYG_d0<@D)HgC>0HA-R)E{!#XpZ0Y92%r3un&6X~&<'
    '3)9LP<Xd3WmOM;1qHdU5TnfuYNZ;9@MdFqJUj7K;{_<BL+HR_ir(wLJzty}HV4A|2)>loh6bO4nCQvtOV^g+#7KEvbRT32+3UK#I'
    'oAjN?RjYf$E(o+felBV6T~X_TZOXGZe}zI;L~r<N5Mj5Kj?^>n;4c#L&GntiQ|=mNZNMA9;m$xLZG?45RPfnvi@3}=umlG*cVvuZ'
    '7sN>&@+c&Xu=ycfzPfq<a$>^5=&NS>7#Q3z$X#kwoZ~7V@=lrORhv003Qcc-jJ#^Yq0sCR~L=VQ!^LM&ibY?m)B0K)=sSELWhkON'
    'Uaq!CF~^ZQd_O|(9Ky}#$Vc*@?~Z_G5asyG@Zwh4X=(&LLab+-H%INoiiLy&aKW)GxxTrY;o9YrCaOMzc^ET)y94R=Z<i!Z>t*hk'
    '$lyta@9FlCT)uYmux#qjGCGwD(C2Y7Szl(Ez;AYE%u5rt3sr?zB;Hlka8y{jQy{Dm`xOTuu$NS52lCEt{=Uu1TlZIhvyBM4AxL;J'
    'cEXY@Iiu~;J9vg6cA(BRi)yde5KlVh{-tp9?7_W<q5iM_x``?ot6Np<OQHrC`*mtfCmP$^GTYE<)nEfyJT}h!b`hNOk><2i2U>&+'
    'mP4qdJo3vI7Fr-b;LuEix3=NWE$b?l1PXCX7SMOBS?3Jd?2ra!5%>oW!QptJC6|Q-<)Lu3i*_YWij!pEHX1?;Y_LfI@rv<&yQsgC'
    'AfBLW99C~%3}8a4M9QyO2RW}B*D*mIz3dea6hs-{K0JZg8d@<gGyy&xbq)GNVg-=sU3ou1Yqs@xvHDnAG@Xw6sI16JH>)?bQ!dI7'
    '!Fua`|})%Xq0Q%8eUVK4n@87)+q0#A{vQ#yEDJxWbHH1c04VPk0TNo?IK1hQ|C(^cI4Es=6++vD}?p-chpw!{W|R+e>=df;zuoOt'
    'VzG=olHXk%*ac3f5<d^H=<oH04}o^K1Jz~ZPh;~+pnqr`Q`6czbAVc4A_MxmQBV**M<;$lMi0ZQucYjME4MK_mfW%J9RvNe`6i2B'
    'spnt<ZzHbxP+(SeeQTGi>4VpJni@VqnsK00Qv!#71O)N*0!VE-hu_{7Ge3!$W@cFha6j)L01gY62byPpPEet7npomSGvd#6?>mc$'
    'E;3SN4xmHXYF@chvC1+3t4_%9P6y-rSPDomt9BqIIEBOwG8!gTVuEEzT$2od}GZf$D)kRt&tAN#pI&sytz%2PtiKWQMh;2&;WR1U'
    'fc(^ce$2hB{?o)o55>YrSPoNl*@{L**?EV_k<lqmEaYXU`i{K<(JRH_KHK09e~jY3uW`!74B2`Yqn15^K5nY*dsicE6-hxXFqP)_'
    'As}xI&pN*2fCR}Qt%&u)S+vlX_Hr@E5s?Z?^O+jWqzG&;=3e$9x5-)AEUo-'
    'eFedN>FFf%;)5V=(B5o+iL;J7jQLn}Bx!|dxykxkR6knK>ksci)-bX?fMXQ-DrT}AK5Kp~iqC=l%8t6y&8s$)^6Gt}#0gB*mthFR'
    'SIPnpeyslVjz%rc8hl~fG8CN3e(DvZy`b3~YeTktVZ7K<_#0gH8Syl(vSgl+lgEvAmAYDh`M)o?xi%}(KxFe$Q2;wzKaRFQQk&*e'
    'T#Bhx(0wL#GN86IMgVkg!uX}j$0ecdt~4Hk$CX>&VKUmX`S3ZexAo-a#w%Z~a3}lF)R%mpqfLBe8dlUKVa=)>;=ds;c3){}Xse}P'
    'U->|bb#uCFqtJ(ePNO|T7fn4~4ijE&E;_ZSsG@G(HTO5Nv*`i4)A{XJO}udol}>g}J~mbgbqHYT>_6qXRyo0cBZ6yv3R&&BCHoc?'
    'YId+Fk&X7hkB{H+V-hQKuHU_f^(US*_--*%#_RPFr_+GUUaQd8+IB#`U{e0>PqV5&f{TYdzD{gCKJLo#H=m(UHMAu9CHpZ>4io#^'
    '(>wC-V4emiV`H7!3{GE8t01Qr1%#7FdA7ATR&Q;#Kd|p^PV^@55W-F1t_f`7B#?P2K*e6&hm)Jny9L7|%*5*(`VuV`{6t^SPH)(O'
    'MV<gdYM>aGJ2d6YRK$9T?iZt5m3u3kd4i*?Uo72%HeKn;Q}k8%UqfjZ&X)r!@^SvYTTJJc6Yh_e%y7n}XFAW@9sbw4`*8Mm@gCvl'
    'vp8A-jid=Wdjrwg*0|-A+qi@09Y*XflsTl`-KE+7&ACo&^ZcBkdLquzrHlPHEzYY`mRH`<2SDKO5_r^8GYqL}1pwuNRgCHCV~hUl'
    '<o0Lo_g5e!+a3Qj!GKM+-flK54BpL@v~#ux3F)|L?w=wr$*7^&e_wF<`lz6$dwdX0pRap|aMV?Y>-ABZJ3EjSUrhk}TFvhDLrZTJ'
    '<rR@+(A%n@SE}S&_Ezb0iBcgc)q-b2EpbW7HvUpyRkk4re6>}0P&8SSFSjl}M3)J7UeJ8$WpK9>Gg+ODxxVHV(8xWE_Z8z;PD6~z'
    'u~v&pBqK#o)T<ll7}h5x=|oDmQ1<@1`NVH>DQ9-Y3Ha~h*A_hVKnG%o5OP|yx!_IgWNTlS1*{-lWU!*?sJ2r`8@%YzxvY;6=>KSQ'
    'q+6J?a!Y$1vJapD9n{Gm+->{7R@;hBKQxf3hQ}Rmza0r^K&DD7Rt@#IYW_7Wd9xRbgVlK&4bVxlLe5S(3?yI{pQ$ZR8L?cF92$Y^'
    '__zmI5MH(}l8@TU|Ln6}=z>+*j+}k=J)39qkJsA$*;#P(U{;8j>eL8q;o&SLkn;uJ1@~^fUFnfJ^LWY&%bTb!nZMsZR~neM1(_?}'
    'M#sh99{k&S`cahKWDW%ua+^WLeP3r&0S6kJG}l6yB>jG`7G|-(%{+EoHYrq@y8{?o;d-sH-97mMXkW7IvtL03;8$nx>qK77c85-l'
    'Xm>{})R?O>x2Z|>17r4*-v#H%(h>;j8XEqg5$rbz?>|2CMbo*)<EKuay6`Ozf{W$uHe7jn01!epkNaY^HN5$J1pwi~{hXXcThlTl'
    'A8f3G;njYotuya-u>~38V4?M7b>=Poi><v6bM}vawSW9=y(6y1CM`}BG{|l`Y*BJflo>g5yRdAhr}Us8jabgxMCSaUxO`7=8(>RY'
    '`t01Kjn<42XQ}B1`>Mw9^PlIw%mN=Zn0>!nbRK%rE+L%GWlEEnzh3TLM8cH&)Gko&xyZjF{0Uw5sQV<RQjK*4Vn=UR28={0QyYaV'
    '%Ae)zvvyEtuxqgDglh3UyA;9WVh@s*fZrAFe4wx-x4E+ZfiQ=@a^hF(VyfSB^t?-b<rs-a{dU-7N-im1=uJ5|g`>Nls|sY36Cps?'
    'cq#}zn-jf1PudB`IMUK-AbV3|&vOs9sx&rH8tbT2i7z{U;;W{w8*`;gI5lNFeO@g<sIR~*6$oe-z3htN<qwp|jbV{^7L>G><XCm;'
    '+M7r`$#o=)VhgNQ)_^8udztl`8ZH$E6z<yd*TKl;WL5m2%caO#IeW4<IT;u;E$Uq)ekFUOSwR-+?enS3D%BXAx|+>{4^8c8tm@+a'
    '#b@sAu<C2~1y;O66YU%KUkg{OP2%oN*A$l&R@C={4_>=^_R1h6DsCwprs7Tk6hjt&c*Ct%?3E!!OA#}0zTn3$Xvg34b#bL9U=(nT'
    'Vn4bBmEkE44E>E=Bvv<Z%5+_2E(#4Z_R2R$TM3DPb?$w>v9^w;nV}g-9IOph!rRi)fPI182cQ_>0_z6A<=P{hHXt_y8w1;{<EcqH'
    'tXn$n?MNG0Fps4c*cc=!@)n&ST}1Y7KA*OA_5o~0utF({6PT9Ngw+GdyZo%!>579`<C}$9c@_0Sm<y=5-fn?f#8cPDB>^OLy^TL{'
    'gjK}g*Pw$UqA%)xMiIZ_K<@_)4PlCzpJv4eOt*q2lkZ}Bs@T;FEY<o8k<IGgxveXAHVTDYd10*B|FU#r7BC*Pyu$5SI=aUhZ@1d+'
    'qPKc*0&&aWbBm+vV<y~D0Vqs!e2}+UStxZQmIXM$d)Dposc|dI%H-z<M6sik@0QG7aBVfep3hUER+YptstS-'
    'MLw&|VdkISTh;S>Y=<-Ghk0|Q>yJAxU7@+RNS6?@u%=Z4io}A{Z!ptbRsY~-*6+bbj>`ksZzmrQdHo3H@nNv~Ij7m;nlIj%-mA-G'
    'ek6L=`y7`KNnt&E}<R%>j;S@;pUK+e<<JgPd-+{q6jaV$B53_0p*<+}*|MFb(*Jm$$`pxmLPhB|XEV6ZegHU<LqF8CSmEyr(oJ~9'
    'R!>MfZ8J60<4?m75g?%yw%Fhu*e5~%DARq+fLIpa{>;%{_Zh>%Mc9~^GBE{0{I_uE%jm_)#_UV}MOIFUDm6y`4Nd~#}s5myLT2nZ'
    '5(00?=sm>2<vyg<S6PI@OQS*Oo%6)k1^tZWRoNYSu(dowDWzauia?J*gi(`UF91O5u+h+TNUw>>55J-d-B1ikc&)*R=c(VZF;q4T'
    '|ByxT%OvJB0<~Ps{tGz|$n|Rh1r#g%4H?;-T)NpM%n4nbe(=IVpTirzkj4_427DJ4^IyR+ao}8Av%00)XPIO=y#Z8Ax3_JxJ645t'
    '~EXuq=CZKNoJwRw-^}4Ek5Ue&g=Rv7!T@1-$xA>?T6c<AisP*uV&V3O#IiS6E@yoB&IL@7mUqS+h+lt!|p$+(w$q$^Bx>_v`LK$+'
    'PH)`Wht~quZe~_YweB67B1DEXmkzLshn&MR(?q8?H_)T~ferN}EG5^c1QRZI{toJ5u$8CVU*M6Y}wCC#e3T+`xC@J00rGShX4|mB'
    'ddY?5T4e}@f`HId5Hc>=eeEU2p#{un2TO_FU#%f=XSd1+bqWiAtmWUkt+uJ4n7j?(G(97%R%qFH9nD`d0?EJ`E(j`xK!3q{0r0$M'
    'QcBK3j!)W^2QvO?=60hkm5f6B5MNjxDYe=+1_#pY33Kc4uF<sGlxm9mg?;VC?u-ojwA772xFX2pq{v2-I!*%;<^{R8OViJ4I%0j6'
    't#JZ)+?Yp(9@7(Ns^o&f2Uy}?R=H~F_5wRjGxOEGKTRWjRM0zRdJ#DC>SwgrfIUG@O-FZ@8e<HvkwauJl1D2Y=kDXUO^Q?;TF0VV'
    '4-Du0^_;h)3Hp2AudyH6ZVit0&kBb(u*tsGQSfwql2nK+>K%W*+WyilUi<7yK31bRrbp)7W4Mj4slQ<kcIgpj+mdfj+N;TMnJ6Fx'
    '#td5|Fu6m?uM_=KndVspZf&E}-gviZIkDoA~9AqOXGyumiOHj<oyLf`<Ow^W?lRH|01Ag*OR$82o<g4E4`RiYOl2ag+1Eto$eRHc'
    'TPcMGYkO19gKte_eEdIy-_w(O;{dMEHZ-06$a~(kPwPhC?f78_XyT)&U7z667JbT{`;hY5|AS(NrOO~3iJ@V2!@m)SPW;zoUStVQ'
    'BLuc4PAE=YM+muJzntWmZYd?n0otR;a%-4l0r5pD`izSxvwQ(EyNwamQ>=1>8|G@UiKo9$ze@gYgz>)2x@M|xrOeo6AJos$>>C?c'
    '^?M|`nLXmg7vJF^wD|h*6Ca46PNhY#m(4Re)73XgYldesMtitj*V!gG?#VKy5-MM*(OH0p6<5#6sV!>6of=1zkn!j#OLd`B@hioy'
    'AO`<$JliyhFkCd^PQ}c(-`}yR6-LW#}V-(Svy5U+$SiQ*cf@gJXLf4i$hVE(az=mR$F;<?$+>?Fu>8C*+;+S9iGfcqe|Bt!;|L-x'
    'jd;tno=?UUuC%qxT{EO^a52i(r_NpbxwknsE%LMQeO2h_bwmNW{Yh$3l)-ME3f7LtM^bf(_H1@i^+Kx$%uZp2P``%pggYO?vmox*'
    'j&BsF)5p5RkXFoZ(|LpHkkTwgLB;mMk#g-8kwDzv5)X>&qXCZ$Y=nFd;FfUOHp|~*6=ktDj0q?S9j25n|lJ62lhBgTJwjit7i^Kt'
    '`J?#xwud2hfhYuFk_<)$=VfwBu`?4bGM&wOdn+Yf_;+tY)0a;ouddJ>mUYvSROy`rrUb2nu#vGm@tQxz3y$-W-xli74t>fuCh?b@'
    'n#2*YzvaUzPR}(a+oD3c*iE({wE_Hcz6(Fx((&veh!wWYfnh<e@*y05+ctZELX$)%utGfGPnCv!%6_QY}Jv2R3nt2geEb<{s*E<1'
    'P%cu8Y?)N|jb8JX$ZWd8F^HYD`-a6W$EhOpd61Nsw{gW!23%Cs3*@SXTH-N!a<F}X}0*V^i)^I=a=T9f~P^Io0qB&GIpz7K7SCK1'
    '-*1z|Tt|ui{M58zLhvc`a(7N0WR}oTF<5-F~r}^I8#k9qCg!^uH#&p)ECjt|iyiC-8S_39hD+0IJZsR0l6V_zUHoldvDxrpyhdii'
    '5Kosy{arPAkz~RwNDGv@Ik6?!&yX&J#38eIDum>1?wS(M+fG`X^)fHen>>)|5jrz9*v|W!ZP_<RcKLu{~4V0}a%-n>QPZjem#e65'
    'dsw~S8GcrF+25mqRl$h)4ML<jSg%mTOVu?!`qr)mPBotS#*adX!A6f>OwzG-G*T%%K9tFneuGr86SSw*v$pTSBEAtcrk_LbL#hgQ'
    '<J9<Ubz_+L=@pNzg#?d`TZ1Qo4_DXdkKM1qs#nWD0fnTIRMMVn(#H4H*R|JAyTPXSsq@?I$uB6LH7f1Safe2$n6T#>y^?F@Ov|hg'
    'x5qq#v>>_BbZdGuldbJuSZ*4M5Y13uNeW;!Q_`<w?r@zmBjvTj0@U?kpYlQu^=h#NZ{>N9B!-d@@h%`JuAQA$7gQvoh8Wr4~%w_4'
    'LqkABEIMlBa6)k+2Q=yqqFAHjnU_kQw+X@F=Qj08vD#xrdcF~SwWlawRw=qu6oor)$%zTT4mmW3qpzY~p^JJhxM%$f|S0!TfYqO='
    'ERw_AOw!h#YX<K&9>ZlqE*W0#XeyDhFl@H>z+i=>L&sz1)d1Y_N0qI1ps`!DTpVG^Yi|OLt#gkwW@|r-a+#AN2G=Zb`e+9fWj(-a'
    '}uD8;_!2H*_LD$&ZmDyiWOiq{dj4r1%T68fEW6}%P)C0~vCfBBP@wqeXIsXDG3sTO3`QwbBE->>*5sEqC=5sq0zrz;Vw4Siet|Ca'
    '0(sx{NA%Msi2c87h-gF5;`6oshmDMCob2yi)S0F@3)o||@=^HOTxGd!dp0!n444R^1|9vyS*=L{aF5MCt-'
    'jIrC`7Y23<VEuT-6Eiz-Lo+x@5ZEyg`c}ndl2FJ#@Yj6;HVRgIrKoBrrD$Lfxhg^b4_O}e*I_v_fJ1>IC}Ir+*$jN`LbNl_G;}``'
    '(ulV7~iM>TH`UNjyh!>FvZcC*JJ{6+1C|=hK^o617d2$ry&wZ!hq@k04s;ha?q|T3<!)EYOzr~_#!*cqL4PfOO}ymS#m@xny!SHi'
    '2iT+{IFx3!ZW}f+8L%Dx7!4JnH1-eOG)taE-;}Fh)RgeRs%ZSkWB^y@?Z`u5wyKxG!RpReuGnV7paS=bPt3P_w8P-Wu?BepbX-}8'
    '7}p7y<rW?1xe`8QN%}=ch6ukUq}T-ay23lxhf$AlJw?kGzD^1DpIx<oKP1cBtnJ<2^!8h^>Az@;kERdLol$)ccadG^JKL`AbF+wj'
    '5rx8j-B?%Yb2-C6zF<WNB8hR=h05SSsScvxg_I}Q{eNR2Xz1it!_S<c(XW_#hxf(=oCyOs)E^jjY?G_QKkGGRD;dl6-ICiLX074C'
    '`Fs+56$BiG;P@&!zX&$(0;tDXM{8L#hkbK)_O>G;=6C<3b^IjvC`=5>m&NE=t3&zRO7ulFM0oC6ysES8hu(WJRu$3Z0X8irLB%TA'
    'm}ON6~qV-Y(r~XF2A@P{y2u<bz+fvL9h&}O7pbA>j5qu*|qS{(HrFUt<<5`Z`-HY#EJ!MGjHZZ5fa|xIFb*X)f%0irLNS?ml2R2X'
    '2`&G1=3FY3*ItX`gAE9B>WgXDjp18_Iz8=<<TENshFpTI&vg30v4Yttq{hm0lFa3?MoKa$Vp4md7k793O^pi*jLJT+x2UStm3|Sb'
    '{^s)wLo!ycmPDw>nr71zEplh3Mt3LR+SS=6_~?H&(=$mcaPN(br~4Ii_6bwVlF$Gi4EacX|w>1ayodT7UGxSgrfm4ZW6xIty{gI{'
    '?E^)wSs!Ob-0SR-3;_*$Ls*!oGRq(Xxv=5lk>(I_%`?RIRO}aZ0jp9>Ovh5jUx0i%6YeNyvD+DJ9~Jh9&8q#9o@sBM_@{BXJE=Pd'
    '}t@g3IC7+nR6)?PG76WoE)pe2I5i#Di@zfIaYkiyyt<+^7c0@xz(%Sx!+UoLa}nh@~~;MtsAc%$E7*DtXv<ZqL>2R8nUn!L~IN>&'
    'Frkav^S^C3S1@a4_#=oQQXu@f;T8S`I(DwAK7WpKZwSzuw-z<@uPd(B<gy+7<69kF&jzKlWy>wrrrK?19e2bOjRU-!2l&3O%Ob13'
    ')!k~2bfV<xloan8W+`VrHK-iNn~??Hl1&)3&<=&yb?^7&6K@uk(he&-|X%DZ4{XuYpq|ay;jMY{Ed)DYsK+J(j+?;#Z{-?_}3oHI'
    '|{(0rc193jx4t^xnFfwl8(<y)4h|MU7d8T5wP+w3dFVMrL#u<7G3%j$|fIDPJ|@ESB2by>VM*%au%%^5>o~>Egw4F35T50RfT9({'
    'xG-t(KD{cD?piTNaivT*Uj9HJ>MgQnY}8ArVl^TrxX(bT(Yi((%404@2n1@{oNv`={1{zkN)oG$PUlVJ9!~wGk9gf_Q2mlE=fBb$'
    'ZBU38AQ&im;#mc!;S|P9-Wz98;u2x8VytO_z@#7*#8aul`6rz7P7aMfOq|5P3@PUv3@`{e*JM)&Di3>z+Asz-#O(}%`U?ZCp|B~9'
    '(1ia>QSB#WfbuqNK9KLJRTG(iGlC>tJQE1?r(hO#PO5+jvU_KwC~*mM}D&J$h!v)?>q9715L+|96Ist7mbw~ZhR!BV*H^h&yR3Ec'
    '|2ygYaS4ij^<mi$jY9Y3iCM`qwWpa^XuWAH=wg$v!KMMeddp3nw5G3EH!#!V%mD?VsXBuxHe<o$-$wyGNWvdSQWe{xzMpgD`z-|Z'
    'HIaz7Z4Vr)F{q&NNx_LH=SsBaQ}*i`!=NQ4lj+59bYuu^od?%ji-hI`R6eoLE@-7^vpPZcD~<iy{HrvIHBCU-<t-Y!kfU6ytRrgQ'
    '?Kv768~m<K21||Hd>&Gy3k71BQHDW=Zb~vNcCJ>hG(E770qMb>=h21(Dl^=PO!W;ft9+bPQn&0u^gB^P=vx?i-BX{B7>X$n3NxU2'
    ');sCk({}VnOTFg=d;ZPvy_A8si5v?F$l<GNmPv9R557fgbWHN!GWDy#xw~|Z{tm4N@{9~l|#`Qi;ur!6B!mQ`^dyehMaTwKvlNBy'
    '2Y%f5sr`S`~qe@se7npj0as{w@#>^#<n!Q)2_7)9uzsY!g&xdqwLE9T+AOt+y;5rVz?_)t`LKGi|a4uaJ2rCbd^Zauym(DQZB%k-'
    'ToKfTsRHga#R5SuJPO4KQ}e~=A{3(>C8!+-tW8eptzoTl=F~h=w5E))gTJPL-({v|Dn10%hOHSM~$cLD~;!fleaCm+@<^3Cw`~PW'
    '*3^j#3#8JD0KD^?nMEl`Vc=EpFZ7qyeXml#ECP2G$oN>-z^G(V`r&qT#*$XRj$q;OlMmTEUg|N+tF1W+`U&5-1WSfdPo>aC-3T<j'
    'JSfx)UN;|l-`skefXGiUo>eMpDS1DA21Kz9JSo$qk{aeoHt3nTHbs-gai8uiUCe_k10}dYk)3nRb9(@%(o_&-IWW9l+K9BWqGx-x'
    'YE8DjyF(VW4a)y{M3BCXLj?|s{uOGW8PD-{UyO2W=O$u*Y~)*c3ZT$9k(ha!OrQ@rSbBEE}QM7-ZcheO+gr(AKaul?Mxf1DmYjxY'
    '}2Z&c>d^oD`8md+3O@LPyw;=vq`jJ&-7GI8j|(teXCJMmI&Q_oE@ZsTUOX2bXmH9KeY4$)Mo}USP-|OytSjqr-m6U#LGx-h|+4kd'
    '+}R_l6EyXs^jArKQAtlN@-`{aftP^Xlip|Lc#!E!VK$@fI<t_z^T|JwnsdGg}$$DkXIeaK9-iI(Rkn0n^~A%8k_uXvDiMJy>k%%h'
    'T-N_1Wxqd+8D~~V|KsnAHHrpbvpCMTbjbUdetAVjIM?0DG5?QxCxpX#JL^X&#nqHs<UG@a({4r<3|8sm{Tz`|08gm=?e${{r>$`-'
    '|6M44?+THJxPlz+_U-$mAh`cNVqX%i*DJe)Dnwsyn!Gew&L}C+KC8qgI}ucM9)_~g83dZ#qLK!O*sL)Xt2XJnl4|SN0Y(asyQ`4L'
    '-mc@T708zVr9NKbqPCg{9d{Bnk@;^XLZ@X(Wpetw(<)QoNla-m3mrbVE(Fh3fE@%8{h!K2S7+LHssjD*h=EE$-^5V&UIi^BAl3d`'
    '+I*k=pgJj%Uz)i60Msq%?NO)Jo(T}k-#M7)u+-ojs_3^PAnn5Vv)@AhZ0@3>p)Ia&Ir%EN@w|+*|HY)wQ*1@tDe=dkD2E+bqQ?F7'
    'yJ3llo9FDeDhdW!ry0_9;=%x^3WoQ8w!pp%7_4_H1xn8+_{;i@1^z(W^V=#n~DuKR}ym9S910rabUVLH}bMHbD8PyUH5z3>6%bFX'
    'mNM>KK{9zZ|x=9LUg?`<Jsxv#*=6sG-'
    'tzRaAQRoXIP~lrKaFV(pZDVdp22K9N!#kp~!QE?AJDq`AJbP{Vl|%1rYBi3abaST$)KS42V--8mRWxR<EjnSjnRXjr9BzsOP?#Lh'
    'oz)a~7;z6|M3KP_RNVv@3k#wL6fEo93%f{(7)DbJs!)8wlTAPN#-ll?DA+4`I2f2XlkpfCbvTHVI?aZc382_Y@~yn2jW{0wC%gw?'
    'uneUK~^gbK!PcMB!r5_5#fhz3Ozu#Dzk4wwnfc$|gWpXIKT)D5+m3kQclK{$1f>%9jQ?q%Km!Pm&1745poQ3$|HeYJ#hu*uWOzYM'
    'eoVg#dzr5+i|2uoO12q!%WMH9yP;2K{XI5_R5BN`P-Jhr{KWOsH$KnXfuJ$3jsLwkR&V^|;4g)LOfZJvG>PrM;nP$aX5rvc;Bh&$'
    '3)?l_wHPw894H)CVHYM%BB36fpbTUVG5Odz^wU2`iBHs_Gi+GrdS#Ex~L@>k(gd?*h1D>@0wu!xKM?y^eGG)S0I2z`=LXlm|lI;Y'
    ')H!Z9Z%bZ>}O=RCl~SJAb12Y!mgjbR@^acyoOPahIJ}G@Kv|;cl08uyL&nBaH5zD6g&pan?V(x$<ChWm@KfG$Atj-'
    'fYEJ1gl^^9Sg{@KB?9ev5@h>kiIK)C=r=`-!+lhTDINbmHVfYu{tXr7Eo)h-%=?N29Go`aOM{0?PmH1f(o0G3r*(|jP?KD-kUZ@b'
    '!O?Je`UA(#x+z~$Szm+sq1L>4cc~fak@MNcGWo*wy%_+#HcMKI#aSuVMj<{2?3hGOd7BdW-^n6jTw{-5$FDsx}=o)bpFCw&wAFo_'
    'PclP6vE}IQ#Yb}2&2s0d%yb~*7&SvwK%deQgLcRYk}(v^<}Y4ju{b;ZVhNtEmmhL2ZwNEoY$?dIA@5aT2#>;-!NPrGM+NytXgfi*'
    '#7Jzs3py(sl{w_8y%h0;MZQU*H2Kez%{@7om!53G}7r|H_+l%Lt+Td-0&SvsTXGgDmS-;%;Sms!F>T>^UQ>m_e2J>{2N3Q;n?ApN'
    '?%D&HJC-txi~y(Gre$)jzqg3M_x^EACN%{7d5lCdU?D)cwC{87A4agFvyLLrc=jEgB*U`SX|a?o-EJW6f@|!c%N^sk{j$E^Hcm@y'
    'kBSv+49lM1i>;Buo^Nk&-*Z+ZycXScPbr)BX%(C(IwVvgn?4{kV{o*6LE7ya_6pVUoqBN1{uX_-(yD_i--9>EK_8IjnhUJ%c~XMe'
    'i6pet*?~;bC$r46Sr(8j*g4_g!378SS*L~ieCX)_}RTn6l6T?7$Pnl!die7!Dm30QQmd|Rp>k@xb^Y~bvF-|Lf8PCJ_dFotu>`Ga'
    ';2+Ja>`lLwRTMAu5i?>bTI0IoA>NE+`Gh)D1X&=F>{1A<5IHVh=&w}BU)l_p*{kg2+!5(0MG@9wfEp$mV1`0D>7dQU1w`h6?q|BP'
    '%pQbhsK~I+;POu(nQDtorO<;sF1lM<2@Q+k}rm3{qmS1-WE4K;vyKsRgPw2cvBOIoDI#SF+Z$|A)3`n6FnOtO;z<Z$MHCq*5;Wev'
    '2Iex%4>`CrtOIu=d$RY_CE)UTrZ3QYsK@kpP>r!s=~))Pt~VJd8OerMY{$G=x8kD0qt-qTQ3a_-1WSc*TGF#tvvwOg<?rm8YizN{'
    'EV`I_eOA($3vfh;3&!7wX3Wy;|YZNz^rJpO_eJ>Qg%{dKd2y_qefZg>9g<xXA(zeJZSGnpgt7KFKRhT_>2CYQE$Czh&=hbAZqydp'
    '*WEC4q!<}*Xi5f_aD4$q^E3sYi~C>_2kiP`~nt{UOveo+t0pR04m}VS!J#SzVA_M!tm{~L33nlYio4AKZQadedR{v9wawHz|^b3;'
    '$0^^{H(elVGqoe)8|Q>Tfg|wQ%z1RY{lW3!p@=i%z3%D)}*He-}VY4)1kF!9L)P~yZ5~!0|=IwSK>F84%U}1G5*U?Cm;jjKrc<In'
    '=%>)h*vMeMxt-Fa{3}L!;|03$puJIL&tP9k-oaVz!IYR<f)ZASX|=ZjS-HWK{Z$%vLvl?J#3M#`6D(O1-Sm`b+ML2>q8oWa_gE{K'
    'yemGXFD$5!n*^;DMDLb%<><PZc;oNMnnVa*{sGGXbB#j2J+}pKEd)6W!hRt6^gQLt$&@%xXHB9R?=Mthw%fk4+zN#F>XzEr)1^7+'
    's5oS#T$7@h!?=MF<A?07r6C9eI}YOA?;(`NLf>WzP%KW-1avvkY8BK0)DET+>V!b{o_BryyNAzmw)x*j<#R^>gAW)O27CorM8z}?'
    '0WGZc9wrt?&|tw)Um)=v_)`4rzmrVOdN}RVwvOQHUC~jh#f5}&sjPBEe?SqI%xqhq{YT?Fg-@5dH$JFZ)e0skLfte$<sc-dQwGh`'
    '5EyAQ@VM_uAsTe+0!Bqu|L1OqorS1MN*suUu~L&F1CQEDB#j4A)+pi${JlX%MC4Gf+~o6z=j3%^NU`jmXEakb@w}}UdZO6%^_++c'
    '|&;mEPHi949}X&rZm5&g+Qs+e#tU*G2vJYSN39ukgLn_kEPkxW)3pEs_JX<<Y1~M&xmIU<4BAweWF@7gKLM-KomVU?+wF_XHyl8l'
    'v^-U1(*mT+11a*RAqp8Ee)BS7P!jbaQ|R*Wwu!ALdal=JW$eR_&7Ho%R>nxa6Brn4gi2r%LyvM87GDNZf)q$206-*bPg;~qbp|*T'
    'GdbO48XG5_f<JX#p~8Z6B%89@MXmG$Q`Pg*obl^sKB-9DU+)@xKMAXa|7c_!&p!U;zvYuEqHr_)P*i!*AgQQXiplqM(Sh3@{iZc<'
    '-PBg%O3-IF?6kd>z0@jjnR4e^S9l-A|B-*+e;t)TBM`=>#aV#@c+2``?AR39uzmrQor84YoFKGU@BqCt0wkvc35PFuWz4QIXjgay'
    'R@|sGC1&n4mGh^QTGpGbypvru8*GfC1T%EF)qM{B3AukyXGusV(7iz<Glef$rFglw(j1KWz%l&tCo9r_x1Gu(RxIzoIUX5{w?_ig'
    'kJKWZU6FS@>;2~qp!DpYj3~I5%fj-)_$$`F7=|^gp=a}YxUZ>U@B-;A2a9X6UgqNS3n;*+nlk9o2&l#9DF%$m1z1}(RMK#77-*x+'
    '+S&#perS(<)T=#lEEt?*%InS<JDrQy|DmvNcJ_<w;;%Sb@_5kwk5_c?+xsz0<u=ljgnsr!t>IBB%RKr6Tqs{&i<`EAIVwj-@T`^z'
    '4N`&j-GN-?fxSwXRCWkJv|>h*BR9z8k5!!OIhiKlz9`omT|ufk8hwigxjG(8FN2l-vuBkY;TA&#fg~R8HB7dkw+$|`iB;*VP{fVn'
    '0v!y=)&DuE*&?bYG(&~F1uxL9*zXW0u44;!Z&}wdB=SkizpDPf3qZF08$ghPIK2q0*lAYk^uST+0!AOV7lho7y;uB|4-5OVO!ECN'
    '%D)1w)N(sm|-I`q9!(ygpV$#9<F&N@+AX0_S?zt?bPanRuGGuSUK~E{Q^~2gMp|v?N_kAV4R-E7)zMd=Qx6F$GMRhTJF~U>WP|Td'
    'sc4LB5HWH#86^ipm-HfLdOlR5(-(q!y+lcqdr>f8?bQa4zbS@2TtEz_83aW?~C9SiDXeZ8#1w!2+K}^5K(8hoh@R*ZxLN%SP>AA('
    'Z=0*O+9G%Nb5R8zMv#i(+p-7iPcb>G_2F;DMAqqPN74GyimRIq&8vwZ4Ma(8;4QW`S=*ob96LiJ6LIwLI(kF0cdn#?aZ(#l|#sTr'
    'v8+sbk;7N(q@VFbcF97U0yjiiC~U4f+w$^{8M?6>bJgLJ$xT^=%W`_PTgn>q8;1^&%I`3;bAgF4`@5i0H#zoN9SGsdz={%$TG9cN'
    'aMv^I086gBA;brCVz3Wn(keS-DofP!e%%+qBpo8^Xw0{xS8rvR1jT4uCPA!Wt#4vT#N}kLo1K9YiHUy5A~Z5(cxQ!bNiph&ios7r'
    'acwur;%x}4IKna%5&-a$~NYB74_f&4ibW*!M$?#@eMB>pvpD@viy~C`yt~JPPenHt7bxAo}@?$Tr;=#9QP;EiI>%>mp>QAc!FUsh'
    's3fqSGV(8oIlgFaLA%rgK$zZYj{^pzYBVWz42cQDATg{Rt6;Vw+xR-p>sb99y2uaZ19+;Vq-+^)h92ugDc}1Q88W}$mOe_J@%}im'
    '%kR`#fx1u;q_O-oQ&l#Cx05ev<`SAoRsfk(8yCEBToa10F(-G8yh!DX%djq>z&=dB#>l7=lHJyB8lN6IpD}sQ6q$m)bHJMfXJVV4'
    '#`17R!>gHppa%Phyy_~3H^2OAFXg78v%VpXpfCRJswQzeUTO-NshRH&kWf>p{L?C*3NvFgK9WXL%zykG}hhPKN6wQ5{dB_fMIL|N'
    '&$zuDHUu-#Bz_%0+&Emu?a4*E?9!x$f0<>HA-R~h(zJe%mE{sKoL1CL=FWp>L`dO53V-hA8-%}e_-uW4(sp?K!<g}4H2fH0MIbj3'
    '}sLd!`Y|77uMO&f4lI7R+xoWh=r%(71jqUG(#0)ctV6n2%rLMm$wcofnWr$!VQ`KvjK3>8fvf(yg(z#Gdu^G+<GCr0N!y7Fu+@mB'
    'Y)NmCx9mzums{zWKjfp{D6T6a>xPLxdq6989$&K0Sx#nwE_!hft-fkQ4lcvJ(d%s9y9d5P=5K#U+(=(TVR~N-MD*}VXaNS0)^K6v'
    'iRQk>fhekmelWFsgEz9@#<=AM5H3Rjf<qfb|F2mGQDs5)6qrR<hZW!&G6dACD>Vk9Ft=aeQ)_v#<P{sZxdUa%P-og5k*F?PHT4#n'
    'O}MPsbdrvKVQ9e3KCbpepS2(89B;^#4py@j>1)~a*=HNCe~TIyMugsTv){11<vq=nZ`rQ-~qlI1-%$s)>eE+V5DfoYn_}RI9?t+K'
    '(>J4o6x%`INk`xTxgN{(iGuD4RkCQ#wj?|H~3zbA!)(qDxV{MSnlsekJ)C1&L5Lo6uWYFx%A0C&kQns`1g3LI5i4aNyNqBQDwM2f'
    'tr6W-NLrf{5q!^dxdR?ZyjJk9uFZKK45cRJHvjBHg(LI;uzAxdvvz(!zuFpQx0WXFlyKZqK=)IH8h+SYI3^qM^n!kvNdUxuprw*Y'
    'J%0-n{Z3RYsFca=3Ee%>!V!I6Bf-)o(XsJ?2RU0sB)u*EYL59zp6hNG{04SDhM#`uR`n%hNMwv`9~b8uHp+d<`K~fVQ1JDA$>!HL'
    '!6v}`x^2w5Zxok>%+qgTSkZ=wi1%1y*!@pACphZC=?4!$%ecZ+hAlq;3z0uXPNgBLj;o@fF&Y5fnmD7z}4e?S>}gLif5OxY)YOEt'
    'pL`LJp(=c-EEc&k9O;an_7Ld8G89ogL8a}^;`XaPbZ{jV8_Bg4W5+=W`({6{>O;CvCcP)R-c_(IW>(L&!#5Ch!Z~vD5J~TK<@C-p'
    'tMopALwB$A`dr3#FH8~h|P`6uN+VoBITmOq#Pbo{zT-L*<wzE2>6_mNDjza?HDHoT>-_uSU#cc>Hobf$Gp^4+AEe%wY}4p`>{;^M'
    'awR+$W@8P(kv7=T0WL%+63EOV|cDjl<URfd)ki>h1`DRW5t1QA75J;ziYh=kIPXT2i?*G>)IDJA=4ZfvtJrN4JoV{gm5)3jgSG!`'
    'UVdu{ltRu2_wM9wmC486!HV_TG?o@@h0YBW)O>2n+EUDpOX}mKhiu7{s@V&MBXkbNR6%DILx!eemrSbifJ-*czATs4%RXY5$(vT4'
    '9P|45g$Hy1-f<g4gxs_n_>g8xbvCXAA!G;JOWF%R%=(evg{<z%$Ld$moJFAh~yeW$~(?#DLXR63&9xdzblJ^UPwXyut`WtWf<qBj'
    'C17N+Vq7udQW;ekDtrdhJpct<|dt0kLPRIJgk2}Is@%6F*Od6$`t972!PFU;}{My^x9NQ%wc#hEX_Y1_E0FcSN;W@dSx}!<|K5%7'
    'R&;Juw!a!MEuv&YbPJeB_d^-GgKm5!1$m!D(aokd{RjWl~ap%hfhR{K5XS+?kU*>e}t8Z&Z^fT$36lE_vmySz!bYA1jM88y2sW<6'
    'S5MO;dKUuKyP;U3V*|+i8-5Cq6;L#5-W~){k_KHV|ePLqfslEDA|x%3ns|sWUA4l!uGEGi5%G&nN>Z<rHDnjX8C;M@OPf*4LHxS)'
    '!O6ub0&qAn&nH1MJ9voE}kb-$Y0G!j>K$bf@yAf<@^#8G2sGF3yS25vM$WIV9Xl1V$Q{`aSx6y3E}`P_VVz!445+2s7&WX;2e*ID'
    '-U8uST5es_tFQLO*MIyv~Jk2panS%oSVuWOR&i3<mK<(s1Mzh|693R13l}hC4<X}k)?-`z*$>09)ScwMkfB+H0_tAUH@>?mM!ff^'
    'U4IEwf^DeL?o05HL4y8I@!ndZ@%aDH~i=9V=%|uTaN7_3%rm%MC7W;DBoJa`C)&dwZX}?lXJGaVvAp<aCBA%;l2kxWv4a1sx=|k+'
    'lA>3S`Xe5*F+XdY*yS&6(P-8yN&iIay5g%16;3!p*Z+$^o`WGg-GH4QFaq2*<v6(-6J|*X8LIVK;4Vts(pPlQLk-$flrs+p&`#@d'
    'T%OB;lIJxH{w9O_Q}JEwe!bJGwYQir$JGs)ZujP!t1MdhpZFh<$R7fdl5^2*y)k`1|bwVaQ2Xy+hDGad^y(l+Co~nfxJIgF&t{6d'
    'DQo3KX&zq)PB_PwU~|ZX2EM~=R*2a7)s<VL`FCrgOFCUh?wpgu0XRGbwE9YnHH5<6i;nBdg(z#am#Az0JeBGxB@gXSmYqm!}O6@<'
    'M8;()Es(<sLK~6h$6dQ-ulMr=(3w*kgOqIFBdZy8b?9#Q;UHb|KwO;{llQ+S4Gqt_NN9{L@sD(gAdP0a}j^kz`A&99aEeJ_|6O`v'
    'j9<}gu)qPg)156E=WB3KD0q(bK2JyJeMgE@zTE!|E*N|D(KlVuv45@I6+<ARp4j3V5gitC=N<2KSN556BxWCw`4TRj-'
    'MjzRfAo@0c8ZPli9)?+^ViF!VOp+Wo6Px5}es^=7QE-Irl&wK-&o2wy1uFmS`@sxvIL#2gEtV{7)VepU+H4vVT8BA&hlbymdepK_'
    '1x{G${|G;_l+=@?(fg<(CXtP;L1T#YaxxK?j7%Q}itoa};p6LSEIV_w-ju=QHDtI~DziCQ=FnS5I^H@srv&l#V|-GLg-ARin@Zk='
    '-;gqliSrQ-e7UNNxOkY0^FFn6w7#c;+jv29`2_T`@aZYMM045yBeIQbRGXkyfC4$oj#)xweu_YmP3dj%^CKe7LFT)6K1;8d`orr-'
    '2>=;}At1zg631ntrp9>&n-2)Pk~C8kfPeU0-}4I?a6fU{S@qGOUjh3u?XA;BqexLswy)F5oSBTNp!OCt%G}tI-1JOr}#f)?ZETW>'
    'yv!%ZP2t4kaFy@SS1C|EM|m!8o5KmIy>|#?Io^Nl|^2(PdAEWN9r_qKxrA>7EN4uD-NfU%VHK_SD1+ecppV8~|MbE6G+0^aW*aHI'
    'ALO5SIGJ=nrQ#OIAyx2q!f~vpv2F+F(ZkF}{nJR?#|Cb(SlnQpb`)jf#}Bugb;A1(?Id+0f21{?^=EB3Cs=(4?7}h$AZ7th!vyvm'
    'RU=M1_vTS!c(e`13#oD-)eI6&TZiTTI>);YGk7pjIob2dAT31Z!tLVFD`_nw^2ViTTtXaWg=-GKmB2UY^=uN`Zm_HqVhGRK0EkJb'
    '7?c%rVdeD!7;81R_>9VO;TO-bgT28WVTryfWh!oW3~!<S?41v=)O!o{xek3;qlwAqz<5t7G4Th#;hI#GQw&@%T_|7e)TK6=Y9Ps<'
    'N^G->J8p$!UwLEVCw`i}n|1#oAcWG~%~YC?>*TzIN0t4$EtgE!XGgWBe72o-^Np4ph_M!ICE2pvlsrPKS+uTu$%}upsIF&EO4mSb'
    'pENq0=iUNa20eKvcfwyk!O$MTDU!<e9L^imDS;iR-a9DULLsE<FoWH3Ve2+Zj^|tWu1GeM&IN6W**Xboe60&7?c=NKR|=aw<R9Ej'
    'A2^&Cyd{ZdgBiNPG6qs*KhqEYpSMhAKYwlgLSS?Z78^!5WJVWuvpsu(`reA1Feaw{$EoV2}D+))u-sC3~!?h!8<w(ft{e<g5y0Fa'
    '2YYVEpta_f9aL#DWV`^uwc##k*Lu<pALY=rt}6oZ#cVPS9spOLJybPMg<r?ty3sm0BpKqmgFp;A&V|JVw7w-dC!Ru5wTJC*{gMwJ'
    'P$T#1@4J_NB?xu5U}+N{CBbvrdk3kU^*#GGf-QOACli6PhKQ*Mq70Q-w&~7fz8j@XEw8t)6%`9UZHIi}X@3d-J?$RQa|z^wl*OtT'
    '<MEow4DSqt}zC(5gM1GY#R2BGNQ9BsXM`TS!~wn3tJ>{XSe6UUcJDdrLmfzl_Dcx7uGRm-e)E^?llF|J>Qv+g~Z|?04gxQNERX_m'
    'X$fcr&3_gZE<V9I>w9$p@zgKL7BYC$&@da|&DJgyyc>M=N(m8@EN99b~8O)#XboXXlwvG~CFeVGD(ee3%-uLy0`t9Z)$KHEi0j?u'
    'd*IMokzG!sf#|M3}e-zvtiFIIfE<b7(a|6V`J`dua!MGQ{>VNc~+qwEK|-#s17?{71SLa8PS^QD&yV36BVU=SfiF%@O^WMT<(vVj'
    'Tx^H5&DX+ac4*7_?e-;Vvgf<mot_nqq>+zjtnJ=qpZNXl4394*D&2%$5hPovGCqzE*&P8l^y-sSBae8qa@{2OHD>@GCV7F^^UVC`'
    'O9P$Hv0N`tUH9NcF@K-x8#(ZICM;EJ<pU-#Z|a8BCiYj-GX)@E_<1YrvLMBA&dgcEPF!eA5KvvCPuL@pO3+?+P(h*6M^@d#Sh7v#'
    ';7+?Mz^{t1Ut~l14wG$Q14#<w+Uj^*=Gyde$f{NuPx_<f-PNQE~FNjZs<FL|h?yH8@-!9g(+l|FeE}3L$X0>FAl&lNVPX$$Wn4*-'
    '3uecz7Fqbye-N<`6eA=mYu2xkc3!2wlqH?D$5kUpNH$Z0%ge82gDgy|(8<d=rV)(Q9g@;|y>I<6HGum_f1_ooIr|#rn-r?QX*nJi'
    'P8y=$s!KF}fmq2CDsSdrJK~cPl-4SNATJJ^kIK{<3~j`lPSBOaJw${wkcNsYNke{D2+xNS<O7sJ&5^?$Mo<(W_$RS*<#<{NoPJ!Q'
    'mBUKK;=4Rw{>ZC{z>$l<jiTS=za?EV8|;Gts<KIMmUo<Tgm*?(M7MS9CboUjWgGN5kfNYsV^+T#ruETt|i0gULAHq+Q)%oOsAeW}'
    'hvXcr0X_H0~U&A9*Z7q;Use&A7B4E|wVCCr8<U&rKRdc+Nl=UKp2IzyXaGi2BGnvJhlRdr;r6asC=o@P`g5LK<}?vad1GM~|=8kR'
    'Mx_Ik5WRE1F=Tm4d+)D5LFe%wK3vva~WvJ`wk6cD8YAVQqSffOPtuhB2HyC!#g->14sgm=oE>^Th+nQfoS7wQ;OLNJ}srXE24K86'
    'y;sLEpUve@WLA(QnQ4%IuSVhkq)S127huvn&Tzsp%m_8HM3gvID^+ikhA*Psnz&{HUHb`3+hOt7*^}KTNj`xy1lO^8k&U9~qczJP'
    '52mmfM05>#I~dV`+HQq+vF=nBUO48C8e6oU^^VcweSG<3fe#tHsq!)y@{V@q?GiZw;I8>^W`hHncKyo>o-jF^X|Qn=1yiHFipO+s'
    '$P;$7;JwP<@6pLKN%Oz|vG>>Y_q-IO#(SvsyG(5vpD$DU-fBf6;SdBiLZK=Nq@bNgYncewV*ea&E;ouq~4(wda>t7Y0FTaq3};%+'
    'acN_d+L-ve0pfpkA#(SHJuAI||iAGhMZA$HgpI`L3oN`uIj;hmf|_jFdj-C^|jrja3tTjaEXsSs_$)*YlOb5@_HVUymO5SpX7I>)'
    'JTy=4KopLx(kS7ayEJ>PJTrcZSl?5ZlDdN1rED=*j0vx^p;f@)5Or#)A7?u4!v)^Z(ad=*VT8iSz^|{^v>k;_0;uQ)Cq&Hd=uRT7'
    'MFHR(rfD2;2$gnQ*zBoZ4M1>q%YxvEjxy!>hH)l^-VRH!da7L$w1CA;+KFKYVi|!#{k{Wof8)zbq$}yB7(gK^Ryo$1kj%7{r5boT'
    '(qU;tNBHYP2u<;V41<VyCRm9+R8IgW&oB_VMs5Or&-M=AXJRm-$56Q&?clkXi>i`Mq|X(caFs6_^;)SU2{KW<;?Y;)in4c~7~C9a'
    '6=>YKg%Elx6tw*5e5~VD{^^!9n_Vc(k5~c>nnB(z86ZTDy&he$d-EdNFjSPy_Jpm0*vKu?`X9Mx_w!+S}hesnulJOyxaTmOG2I;~'
    'j#08ool3^Q^B@DSy&k{<JN)ETu|+_b%~3+TZ=RZNL3(6FW+aUpMih$QssViw3<XLf^W-2Q?^vzO(&x{ZQFqCg!yX+$r!JOdc~&WN'
    'g?ffDYZJ*rRT+e~UQOhG6n1HnTP8gI7pnb7k!ClY19o5Q~vMaa<wmTKS|C56BR#DNRZsmt~!UnHvel6y5?yiIFAQ1^OpP+xTG#NW'
    'J3`-d<C*a+BBllHR`lWT09G5VqPaPH=zEKAAnV43W=vmy<AcF90W7>HDOd>=>waE7OLjaFV`W{jJ=m@_aSfQQlqZ+4VxwS4sAkE4'
    '%tCdy@Y0&fUG;J4-#u&c3d)EzcGZzZe<I-zV{_;1l*|Q+pR}Pp~=<CgnT4%iZsoVrR#8<7hbi$kQE^7>@t6KSPBv13V0OYa>wuo0'
    '<(Vc}#xB4`DZH2*jfEqOBiJ`wFTDwTx1iGmnr_(R%#Gpd89OYlG)jzMK?666IW(o?SYqaDcC6b#r?8*4IdO=^;xx_g1^wh>8AH+e'
    '<IC?d_}dBO}@d2=Sio-mbPHKc?-ImtYPcc&rL6>8uQh9lfXA0dKwBnNZonfW>tkRXn)mVd<n{#}-SNdpY?(e2IuKVFV(QriWP?0t'
    '<Ee0{qm7&V$Nc?$B6NvV7f@4rig0%<?fKLwNkKA@cDoT0-+d;sDkkeTiy!qPcE9MqWl^b~u8w8t-P!iv<nNX@KS7Ne+&c-w-@{BR'
    '+d|Y!0~Hi38$o8Z1FCiIm}E#OPrsY7j<!$T%qTzD-z&f@oAOikLKiy_tNAFziO}1Z}EQ3qhcc=i1N@^+(!Fa`|$y)s`T6v(($Yt6'
    'c3*>f@7*n?OLOpG;o$g_iyF<N<mCtxnfujj@+cpF>+YE}s!Cjj?~BGey>KofK0(ea3%%_wBdZ|FHGV-^#-Ox4z29)xEN5l6Niil;'
    'HL^4nqN}=B|&~)3{<bcQ0;9%xYDyQFNGD6pYi-x#1zFfzFymN+c)B!S<3ft}7k?n7301w+T#Z3M4~QtIJD*vEf31odyQwNw7Mw95'
    'Nj|^4dQ7svLQud{C%1IQQh?IRn0kOt>f3n;1=x*~F}HNPM(n9y4S6pAY7NM$tAPq9hq2L7+|_ZS6ruk3G>giX0^GPh*?bEE+Ekp4'
    'H&Zjl+n1&3=vW3>sj;nqNDCuz_8sb`H~7uyyrpao!zT$w6ZWDae7J+p@ye2NW%Ifk*5oxLWDFBSoM|qP5dkRxZy%A#v;erqtWjw`'
    '*7OPHE@I<^Dtwn1^JSD5Uw5?k@Pla&gkx+t&+6rgv9&WsiP%`VIp`jJ!4$Q3a50EM!BP-cl_Pm1%Cwi)ZReovok<35TUeUXkNt83'
    'YM`&6o!)Dy_g_?9HP#2MPUT4TI`>#GQD*G|~-7c9<vz+;G2u=nHsw8vd4fJ}zQ2ZpIk7vYIU5OW4$i>)hfHA^EQx0kJ_edDO4et4'
    '3@<O|yV^w=f($Y%r6KbDLnN>FdBU)nBgMy$kKe6tXx#%a7B=_=PNn9x8j1_|40gIRBkJ-5>4l@6@KxaH4s#L&oT~Z5=g;o;(&oab'
    '5wzIw_r^*t+)&>}lI2@{xB?GfMB^kvQsn@G$5}VldMWr3IpqMd3XW)W*F4ebrTiwHw{b$JfeuGtNVG2tvo}1F3;R>%^m{%?c2yDY'
    'BPnrWEku5Hzh%UaHrYISsy<*!S1>YP`%(YU9XVY8)wDjF-10Hb#9F6}Dmy=^q6ABFYe6klCex4SjY^mH>pRXk4Bv>=)f=D04V84p'
    'ss&)($^PLklwB*)yOrGCe@`k?&TohSP}UTsnq|<4XnM2-bT#R-HSENUSs>5o)t;D7M6mKTRPo(%*pzsBw+Pog-=JoCurx=+YByX}'
    'o%3-s)x4Y}7_VSBSGwsiE;26*k~SmXpF$nOfb^S8^}-6ndi&uT)WjB)bMEDK~I3?|hHpX`qNUsN866W~y;|GKdFrBh^Y3kq{8(bP'
    '<9F`;E42)qjgAbL5Jidz0PP+t=`IYR2j(%5$;o1#-5%ez_$v$eOwyI)t&qN#{E+YOeL2m-wGwhy)ksW%M*h=F2a?>-4lsd;KQL<J'
    '&p??Q}Dt(G)cw`o);ei@-}5xPQwxUOOW$!Hsar-+8H%fR*~=gEaQy-C`{k!)~F-z?(n5iRhFv1S_CZ;S#C|M2MiJ#F;xQH-'
    'GSl{G!t;ru5myDyu&?b^BmuZCcLO1o&a5mjb+Obamp8?s=CnTu9nKYW-15C!IS>y&a{lPfBw7Q~)aBlI#W|t`eDZ=uVk)sD5vx-M'
    'OpOQ^f_d6hhP>HcDL%B8v34{gnZP)ghPgQ?0w9Di_fN`&cRDO`XhLu{qLi;9$$qIf`c|B$e2MS)?HlO^fOJ^WM5V(XbpJ%m?wd)U'
    'v8yn`b{aSiRxtb224z6HJJF%}P%luv`3w<A`e4!wuI42Vpm>R8A+GH&i}&>`A7PcrXDPI<dGiHsnsY+8?w>IA2E6q@CKl{_Ww2Ez'
    '7ATw+~FJSXyp~53^RYx;-@x9P#545;i>4xOvYYZQ^mxo>OcS27nZjo?I|d=>XCr{101_2<xj`EwF0_q={KU`eGW=GT4AuUMZ-gIa'
    'Hx3tYbm0K@}-`7(er{y-PT}s>&UNBe=74$^zU&>1bwH^=@;bdMqF}a1^wLj|LiaOU5TB(8ABP@{f$;)LVvOmgIZEMl95_{4le+`~'
    'sQF<!go>Q^76{wrWVY=9+w`>#{VGvqFg!&F7B1+T6Vt)m*-DER`<ex%Led%;Ed>Ls86@I~!K(p%JrvBh%RP!`9Yb&qh#nszNNpF?'
    'V>KHKOuxv_?=M_a>U<fJRc_3wRZUrM2k~dbQKa#1)98<Dph)bVT-Q7}(s^wX@S=SQ%ffg79FZiS(>}voiZQv^emY*hq+QU4us93v'
    'uj;ZkNrmRlB%h6%YX)H^!{#R)lpJ^7OfC6293Y+b=aPAGeyJtaan-#f!-|oK7O`C(W?U%dBbDm`WLX7({u{x}wu68t7v1HfT=K<n'
    'S60MU|bvfl?zRT>-Z|_*2=+q?)K78;$H2dRI3N>%N|_V31nQkPUo@BB6Zqq+7*qEE;!l1WgcB#>P&QK~dvsCZL<fEapecGhLOxSG'
    '*|<45N#Lc4b5%o64qv5CVQfYv;d|H)p2F7cvL9NJaF}A=EJI;BZhhCgy|t`y`GUrvtFVIQ%^`JX~womd75}k6oeh$0VpHB<NSPv!'
    'csPB{yYaM&>kmaeA1!6VT!ZTiNTaIGLRDwbM6JY!ycR&i2>S!CLq@R4dX2;-k%B)lUqG`|$e8_<6U*Jz41KcD#x{T615ztrqS_kE'
    'Wf>=$c0B7^h|dWvx~}vH*$7S=5SSmhQWG$AawGJ)nt5Bi~0JyLLoIar4uogEKWZkJj#Ldql&_MfjsVrN@-P2v)p;sWohUK9||~P('
    'nMzaHf`8;|$^}^CV^V23Jlz(wP}zenf0=<UjTCEc4Bss7M8fe}8ttOgzOEn$cA)e^Rb=_wVa`MGf$zT}@>D&o4UZruXes(r5VvY1'
    'CLZ=}82ZVJct~(1Ugs(I|vi(PgSL^iE|&bzrc*V!37F2{fe(nXf<UR)$>sWbwWqN5+8N8cGdx@87L@NN-Rn@9f*N2OzyLYQ;Lz!J'
    ')#}-}QakTdv?amk!9GdQzLPCeWZt1r;Z_ZIlua(>Xeg3b${U!xMIwcYZ9_y8R$GdGhci4}2P$G2)7lNbuYYPn?-%%ID6fib1zFeV'
    '4W_igSlA0xJDznD^yQ5ZgOiR<&8UeF=gb*F@q~I}H=_F2dkcQ&}rsTI&Rs>JLuh#LRwOe|Q|mq6Q3b+;pQP+g^7*3cPi-uji98@i'
    'lv)GfO=^TCtLT+}qcmeh{K}_^7{J?bqMrG;wuzX>ZvTaaXrYEmo~XvaZFio04^6SJ6o~QG4)vh4>8tcYX33(^+D;FU>&QEZ$#xIH'
    'kS>Hd$lwFs$g4w^ye|0AzrSQwt`>B$3cWJDa#JNmFek9r%e93L}jo>8Mt)O84ij-f8Yi^Vk4m)@m8U-BE+y`M!Q(o~k~Z&D+&iN%'
    'oW~h`?66|JdF8(F;kXuhg}tv^UvX=>|)+alXHhbd~!{-96>5L@BmPI`vlW_X5;If?S~9LGGF6@lejxEr0e8RLa>zUYt!`Isd&(IK'
    'AB?@Hh{CUmuzwWDSupT@OX{C<GWh_gB=lLga#mBmZrH!e~a^>ddbe9ns|z#KXDW>H6E@S58HMd4F&fP2v}B8W?2ZT4Uj&Q4fee7F'
    'V|X?I!YC9HjqD<h4rV`4qM7sC1Wi<-?!NUvFmH3qW5KzG?Po;GLzqrj!o2iN9vz&lV+{;M+iQj#UitK{8&?rzPjIXK!fr+EK8FhC'
    'ko#mEBnSs9XVAG@tgDPR(WBp(!<2pBSl6en)%fN95(=n{%{J$<C>4nfH_pCpFH$QQ|ukiZsgViQiaRIsxPT{OQ&2jw_*om6h>Yo;'
    'qVOQ9u*VPXMv}71D90kC=KW%mBHXbOq^YHUpe#wOR00b*07l3`!xM?p7%x<_k2vbV0-7i;N%HY-lD6HAzz=vcNP5n1!;yx1-xtk@'
    'Jc9)DaC0z=hTNP%5~L72u`^G}#f;|1?8r0frh6J-Int{W=x}a(8M%Ao@2s#RcF&fc1M}M$g4zWY%t9t}l*|!^|NqK&~Eqh&!{+Oc'
    'fw^EW*b~6*3-*Y?^iS(BEVdeW;<ocGRHV%O~s0s_w+Fwvs5ZICYkh7Xp*fHRJ$d2)(TF{6*WTlScx9R-CY*meH&;)+g+dQ1daHBJ'
    'mkK!buKhlg1Wm(L(AHI_@)dIX&%Z^_TbVH5Ny}`Lr#;UA0<Yq@|p#mC4D*<0Y8ytotwv!tg=Hx<y2JDEH(kZ%ofXCWz<|5AX^c8v'
    'u95PBH!u$7c2jixM;0I~E*q;Fextc$RsWG)SAPMxioYIXGNDe=tO7t(GmEx5nj5s94vyIc8LxthvKpr%<<Q$CpD%9FlG%#*q0loW'
    'D8Z8lE+nu!>yf$h_*sHvRFjrs@|B+g_)+)@%`H*ZCi}7%Z5+kw<~K+{z{;`~Gq!4~tjU(7hZ8hyXq9ad`@1b{fMck?~>8bRAzgby'
    '4&Wtw0jm*wlJ$1M7>FPS)oz1@MiG#|7kZW<5rKt2Dm^P8W4V;pD1E&H6bx*2+dQvvQ+WkaNpNwL;$Z*$tP3;6Z9wPO4eT`g)iGns'
    'A!uXx#j^z}9B<i!uv93*TFAEHNvap#wl&!RQkIL^zvSu$cqYD6#qPCv(|WnE_1p{)y@*R6%**+|yjK^k96H)<yUPB#AIAp%X^Hnx'
    'iWx>&<w&J<Tha4nDNmO&^s>p_uc1|4y1qQBJk{X72^~Xb4-T9!GOfRV1}Cl_E3+$x>TD;#Z9Xvmtf65#UptYoO-axIgO3#4K0kBS'
    'uJ8UI35L#GO5<t5Vw4AKpG!D$r~tdgxe*-cBvm&ws7VCm^0nPjC!>@#CCEK^z4VPgeLy4_F*QmLaN#+L_{<G(G7?w_%f87~Wa)x!'
    'LM=03EF;Uf6hy#bZAOzm-XhN0+fBiB~&xb@j_TR^Mc)q%=fn5F&LcRaiz*@ndI1dW2SKU)JwkDuCu~429R68d`s!#IjzE&S=}jiK'
    'BJIHxLNwaAyj#GJZFyf>eou+Ib`}8(H>WPiHQ^?)aOq6R3g}x4{q_Wot8JJ5k!Kv`}NiF{*XND2rXA0EYpC<D3x>g;Bo;au-ji^0'
    'Gh?DFT%H(b@&8E@v;(SgOX$rHv=Vb(uv6=(871&RWPRFu1V39(g*o+sHW*8Dbi(SCp5UV>At9ld|C<?IvS%3;gtfN;YeQuol){r{'
    '>*vasTTJ@t<ai=nG=wtEgQ&e@tv6_Wt4ULZ=_)XW<Kb-7WVgiCWZ<S(}+#J+F`#8i$cx<GB%MNN~f3w@Jxa0^nnU*87vk3-!snQF'
    'B5M=f#!{iyRx557Za!3Nld=DQC`;nuDYfPPtlmhDfjy%p%rZV$uj<0E<BQWKML!MLg@^!^Ysj#`Lk2v4tm(x6+=xPM#KLW7cM-Rx'
    'b~EO|ZX6dO)-Vaa1C#e4pH?G?28Vj<e)EsSU%85fv-$_vpyVjmz{;MEEH&nO>phm32#!12TE4G0Mcn|Hamq8LN&*p1agMkTgTWK)'
    '*``MBK4|PC{#mqdiBK%9mb@?rS8Z6R|Gg-C1JocrvYMda}Hn+k%inS{2RMdX$2URkY_HQBQa8s9A{G{eub}M>W=#YGf2@bH~c*5s'
    'Ho&tOvTLJUMs2qqh*`VI&xgX>a`G>hhwc6fb|nwu+$-66@|F>5jQ)8Kgq`GQL((UONU6s3o3TuAKPJUO>rz<Otnj#8U_bP&uB%2n'
    'O3;lQdbg1hdLmJhdpB+AQ8L3=ZcPaJ@J(6G}|=Ma&tQpg_JhTE$;Zp=xchyG`qA&3uACYb8a8gp$BFvqDxh3uP70IpwDHJEGjWTB'
    'Kb3`=TUE+{QPjSS^{tlyI;_n7?N}Sm`UxFhTafp}K1ir)*P(*&q_a#S}B;>Cx15xaYmSJTT2Rs|W66d{If%l6K@7VjM|th0q@Rzv'
    '*T(_FfsHv7%@|u&Evj^-W#nq?A<q#e8fl?b*@&(LmooH8G<har@wfq`UW%a&N!<7D1!*2V_m$Rq5N4$Sk9Jon=w_eR)Ialz51iNH'
    'NZ3@w1fj#JqCdI*5R1(Vf?XDKs<`vT7}wl|3=pua=9vI!q>tL$Mh>9Frk&S7c6Lcm;m9_{{!Ir$~I`|7TL9RZ>JIrzIdf_uYTiHT'
    '41~F*2=*(hcTJav2L=8d4QZgtkan{AE)o*@662ITO9>hDw=QCQDA-'
    'sgEvM$^<yxt478h04ts~am-4q^oi30SqvmfqIl=oze?_etRn$jJdsbLn0dr~qp?lfp^-vHcoRfBseUGb3+am-Zje*AZmOi^@WeR>'
    '-qZ|JP@K#$ek2tYo~tq3>gZHrE;mU5Gxlb4Sr0Hf*g3v%uGCFC-8fA3vod&@MqXTuW@S84h&7+EVF>w2ZNlQ+>gkdcgGPaouF*b0'
    '{3x2#)Z*4jWz7vuqEyF0=qXIwFh4RqkFSXwPdz#Y7f&sDBs~mujGIVnt5k`SsKQ=zCMHtC%xKW084nVeLiD_7{>(ao>3wh#wLpbI'
    '>H4G7Vl{vhL5(AIjshDcYpJQt3i3W?C8$@cI(%{pbSC%j5dW4&1?R3?{aAM6#qL8U>630p0vw&2w3%Wn-KxG{vUo}_>7xyrH>zRm'
    'uvUvveNyEI%MuXZ`HR*`PrQ0|!BCC~XWl+cUaq)mDMs?dmbs_;0JX9+qXkZIS2Z)nKOJ=KSZl;W2M_!5GNy^1feLufWQ?eE+_K{$'
    'S?Wl%)3fRD#H$KT-m;N}8tc5XHP5j`Y$&wUfKFp@SV8z{iX^PE9yb>&vEvYa5vp<wa-;F$0&W@NJcASI?t=RPPBD`qLJO=5SbPFt'
    'b#$S2FLdvbX#^}u%Q}o+O}nM6OfM@{5y*?cVkF{ME68|L6v8ZyD+%EE%Gm=3G*ub`Gv@SAix5lUvI|#mU{ynf-91LyZ;<An{6;&o'
    'TtSX(O01b1nPiZ01Z9i)Sak!KIQVrhFExY9{amORFsTVRy^$p$ke~!4Bh=EQd}3K8N;fQHlFH)R2>QOK83|9(0KXP8u%141)x$-@'
    'N;8h9<RlK5v^pl`=}oDzVvhoYQ{JU%qtGOi&e4(j=t#=lzWkN@IWdb5!@qd=#Bl^p1L+<c7SIzR*w$FuM~%Ue%_lM`AUTj?*kw{c'
    'nvQ#>;|7hu;>?9B7Az5i=iWUBHpi^Tp~pfR&I3E}<T2yIWETj0tyDGbei)+eJxv*$)K*QInB<hZE6q*nxP9F>?A|tcBvbPoXMFHh'
    'TAGlpbI!&rJg}LmuQm`>p!;dg_M{Y<Q0qZ!5?PB<sp9k%@`T)As_jXng52iaD~+Kstv=A&p3`><LUNwXwf;IczmXFbW3rL^iT(tO'
    'jn1;E|CHIhMM8#kQWiGK;_gAs9<(S$h1vZDA)<m3S%?5tN~k#D8OW5P<ces0v)JsMzs3|7w`OJ?&8Si7;RXclBECZUYo=%zk&o?`'
    'r)6c*z*Xxbq)8Z+2`sM4Bwuv%6r^D2z_5HmOGOVEBB?1w*D5Hi;jh>LV6uf-^pL!paYG2)X4%9zH%R(t=z$eJVT#35H^FGKG=wMX'
    'EDaHMNSw6@sS~e`i)^nMPbtLdg?jCd_*4NU<J`!;k#HVW^1w9JPe?h)MkXMMN{e&=-Z(!}pPwf`79QE!a<j5DEyBpw!!WyH)<L+F'
    '8G1ow(a;MjeaLMQm{AvQHEv&|<__76xG-FMG!~VUjE;yIpa$U}tTcH+D<omkA~uMS1Tn0@ueZ0o_~J{QKsc^mp-egzRsuPRKr3wx'
    '1Ofxz40ocp*vr(je7oE(a=)F?r$APrmO^0jRYD6q0XRG>JD8v)s|$tcz`d3HRe=B?Cv&%6v@{wm$jKiZY}`EG4j=IJjUX6sUle<W'
    '04oz+KuCc;V8+C-67{+ND0<Y^l6i%Y67w_|Da)c{*h{k4WqWd3Lo|HGlU1p{Q_3={WhbpCYk8D!FzoJ8CZ!;lv|?3Z9ZV&@BnQGw'
    'K$XAI;%$8`T|85I2v?J$o=eSJpn|rkbSJ0Wj34cS_~b|9N~chz6YZb4R)-J9G^-Vt#{Ojpe7p9nH`HuvbG_MCBWr%g_I4maJ!NMp'
    'wznxnGM09(tjo_sW`ME5kiKv{+uZPKb5qOE$kfCs+mPhCbEYn3WEG`_jP-?U?VP@L!$zK4i261~rc-=;TrOmr2kD!!xZC5ukd7L1'
    '3C0l(t;s1lz-oRS0GJukBDisMaAf}xd&zC*KFFz+JELx#LG{@ThUW8H&K+%4254_<8^oyQl=ZrC+y+6uV8Q1EXr{(Dc8hsUEGjdb'
    'sM$r1*FN@=E!wAixo#zzAjMCwov~yfNjl~ZTREpKZ&VLU@<jJbGG>bD+JKbt$O;-sOilqwqG68==p+O^hCBfhvyYfc2>=wOuBG`A'
    '*&3m07|4+RzMXwN!Qh~)#W94cZp5x5P7aaFH>R>Qt14)`k3H=YZr`|V9(V^zA0dH6d(8sS+S=kt?O`!MNhP`X$4RI4@AIL1<Gz2d'
    'YW2qf4~2`XDdw+D<lJ(7`bx~64tSG6USXHWE9O%=5W1@HEjsu~ceQ&*cMmEJ6Q&zj<c@b~(}JSj5<+rZQS$U1ITD5c30>FJhc)|~'
    '`BD@B_E!iGB=1-@EsfpBv*aXAnm*y;jtxDzKWsN3-SONNOKNNOgTAL4#zq&kTD!e+<N{N`K0FH=&z`fE5L2RV&S)ok?KN7P=4|<f'
    '9nZYm`e_GN==xjm^gyhrr34hgtk@%u8jr0}HJcuiof0m*MQeuErmc}QI!HMC43j?ETUbx^;scm8W5elCwF&kW&zV?iA{r6vJlDQ@'
    'B!>sCl+Lf%!-)FoN_p0c<`iam`2?8#xe_WljEPt~t2#mixkNUY@r0lU<L!iKufbd_=<4d+Q8e0#@pW9{V0#CD0D2S#bk?*NgO|pm'
    'p<k`63n*Xk`i4qdL&Yk-XKnCs&JX3Ytj3c~w;4;0PNua(L_cWukkwg5x!af}1N|^r6+-PBgokv(Ib#4F($+Cv+5@Z0i(#%N7VGB^'
    'TGv~hQ(~NyxeJrNHgjV2QY~c(q3>!~rn4ZVk2;*#YK5Q&6C)f-bof>8a%++vnEwX3u0{nj{J;3Vv$}J4c~7b1lX3+Vp`9SnRdE>t'
    '2xCUV1#$7x7O}y2Su9XKUq__a&x6S8=<dSrX@Q$-aqduK5sCppcrq(!ri)T_ZLrpObPSC^M^7tc7z_s?Wn3NDE5k-_yr$>$@7r6('
    'lR9(QK&_K4>@A>mwb@1+)xG7N_IX$MzQ0dzjlYbfqEbdCoSgXOPs%-gd-q@pb~7s#MkbHz(GhvblmKlnPUCS1)0-{_vT2tNVDewo'
    'XGDuo?JxBYRQZ~Q1Rlx;M}}gI^iXGSx!hH48`#?wDEoZ|yZoNo$b@Nl4tJg{0Z7u9(Ve{F(;zc#aaRrxikFdU0c-uNvOdxcMv7=1'
    'VNp7hY<t~q>5R3VB(~<`Nn@|2?H0+3VPwP?#TJC$Y;U<2mG9lUPmE=wo!Kq-7T6=oiDry6D!xyj+@|9{21Is*k+F2hq6ZqbQtm&^'
    '6HHC4)^2$0)Nb(~10@`pj^4h0j)gCl24Lr|I?S5f>JV`v0GEw58}<1;W$Z7l3ht<s(WJ9n<t9L}H*Ab*)z*7QPv6du?cez6zP3-h'
    '`**kDP1?G}`lzPgg!M1?bbr(>o2R3<{8@j8?68k)*3?d7YS;+4X1)x}PpNQ>;oW4OhFFTwd}feD5$PG^y8??6@>VEH8sprCaslQI'
    'qtb$z^@|GXmrtx5K7&kJVPQcgHYe|o3E^#FM6{-VGBzUBNIBvsaX)aOmPLmfN2iOpUU0MoDQ7`CjTCr+1sVfbj8sgNh=j=YxVBX='
    'UP)tPDQIAk579IAd2Vg+cx=ovSrMA~=>VD^NVAvRXAaciZ#s6sn6xG$My(LO1u>dzPKetB?q?{s+BjFk`_D(^99`v}@<$?>y4p(J'
    'ak~UU@qPC&?`6b%SyIu?kGp%jz@<?s|Hps`{*d^Wg$T#St=x=GQc0QB9cU@#a@bdt5?7v3`lQs|<5G4{ceUEx`;q>;Z=fIYH9Q-5'
    'gP!i4{hb-mriGuCw}LUSO!<1u1f*&;JRo<^#46iVjHC)7qMS=(?D<<p<7D|2t*`XbjJDu|z2L;!NFN-okNlwP-dZqmKkFTXJW9DK'
    'B5@Vg+rqNWs?nB^Wy(2L(XFwz><1}Ey=8n?<I}i#FvkbW<Wwd@lHs}3fQ$JfOIEDOqiNJkmWJ>N^f*FV?L^d%XaQpo9X+IIJU!o$'
    'rb)2@>D$C!{^V7<1OWjPnAGJM>F0SDXzq#`=5yETOONV5j6qHpfu=%$odYK_FV)SQxzt61cg}Ip4hRgiL?bg+j1h=uqI%DqWXR<s'
    'vq84FO%u-hn_K_u+uOIix$UjDw|6G2x1`Grp_V_5wk;>}7K#I+TWBs`$o7TiPk(=<J_7P&Z|I{INa{Y&W?rqGL=och^6ITxV7PEQ'
    'J2)Y!lbr(VfREA`1XGyWZgbIcQ0l_4qK>;IDYhB2AqrVlws>V@PCdv@#^vNi&fVn2kG}m7fYsv4Bq)15fW;;OWQDd2vDrwEZ|1<_'
    'bZGpI2z)ylf$2JP+zY}ty9f4^dYLTh>k%Wot8JIa(}7AkZB?f;@BKdFf-G%_m1Ylu+@Q8oYl(8>XFuCrs&utg`}_8?_@b%QqOpAK'
    'N$sH~lkT%cQp|O0nNU?&&RhtKPdmLDxSgzd+($8Q<<EX*NHZ0PZ9yY(AUL7dV~8RKMS#13?}jKcf<rqLlT~V*5Ar3)Z*AI|O`|jY'
    '#h~@w@c=Di6Era`?EBfzN?qdK^;Py+3uL^$!Ve}qq#l>##}sq$Nhhb)MnGr8W+r6xbkot!kQ|D}-S5F4sndrRv%1X)4Nt-V<k!A}'
    'iCZn;f{OYs1sh<taK6nZ3e2f<7I|`#i-3RR@CYF>8&Uy}-tsW&mz-U?Vq%U~{Yf&M^iBOq2ufvZDqAnw!Gs$drNjxZ%!py7%{Wq<'
    '>GfzhemWRwY}YuFE9PA(hLx_&&99xl{Zs-DdQ}@I;GiK8I>u_m#Nnf@(C{{Po!AU<xN*})F9_H{ZCE~OhMI%!!OF0;76%y9?;1l;'
    'QNR*VrUtTG5JPKn!H@mQ%z^sQoS%Qi2to5I`RG?E<kM=I)Bv*zK)sR<Sa@XMcF6e2$B>4Ivq~1#uT60Fd6dDBd~Z4QJENU&Xc<V&'
    'W+{4`TydwGrKK5Hw0sJ9#N$adMga=C2oy*u=Zq;~wX(nC%caemGxNjzjk=b+EUJ=T3Y^|~`t!^<y{DODq%6I!t|1VmgiSEoXN|J?'
    'hM^UaoqP?MHuDG<F>UC6qVby{#~`0@rjaY&Si1wXBo~hDe?HQ1q9RzH+&jE_c|0f!r?H@W%U+?1&Gn|$+UE`y)!+eNpoj)qi7K8B'
    'dDQ@{{$bfh_Lw78jKeQP1?l1UPpnEQ(EgFM*Nr){f;iE8QHq2a#`$m8&V(qbX21~TV6)(Sl++rD-CaZl#lxG(nuA>z`AA%(bqsOe'
    'PS_FJOwpPpgYZIWZ?ui4J7WYyQDQsMFXJ!_bXpec-h|UzlUWjxqeu_BmN&|y7>fPXFeHM?65iP=+hPNg#}+nuxUT_XA72wKXVkS('
    'BPX6G*<g*ulPuSg5j0CGJ}>erqQ`I);rS#;5qIeN0ymlnA6OF#a7u$opBIY~#0XXP1*td26^Vj*MxpOi&YUru5VoPz0zD`C=U6Sa'
    'KYGeK7Ead?0YS?*Z0Nu(gFdB**4ebrYEn~Mc__67Z{h?9^f%b+4rhy1mDcG3D#}JRdWZWho;9|A18c{<gWC2x-6ZK7i3D6exw`zA'
    '9QaS}od^6qT0p)X=-qFek3Z+?*|(85?dcicPR9CNphvStz88jBkL_UEdoS*-{2XKU&lqeX(wESLq%#ypieogU*rADekuSQncJ=gq'
    'iXfBO!-FEnm^%!-Ib$X>=dOf2ElNJYg2cL~Z*Qr-yimV*+CjL%*5l(%!fF+hRrV=P@oM4;gKA&TfSR45-+Oy!!rUH%alw<5(aS7r'
    '*v?;AS<0<qEPhoDtDp?jVo^~6Xs4n6d0TVX9!>|!S3J>6C((V8fQh&tiOrF`zXsJAIx6oP=xH;*nsGDyyREOcXJ2R3Y8wS_`BJyg'
    '2R&MSrdk4E&|bn);2tuGWe^XUkyG6~-1r9l2e<bP$PP&~!*>HMR`o%c$dO?SKg7*WeO4Ps(U4A~sOgl#^&h^NY(;beVq$cfenEH}'
    'oj*Ropvm$mIj<_LIE#jr61z>m^3HQ2e&jsaD54Db<l@SibM>V!<<Lew=%d6pAkIgP6^<hVE|X?LWAP%#!ND?O8z;zR5~57CE4b|O'
    'h_)`jsozD6c;yEaH^ANMwDXys*!EBD+G_hcTU<GtV3D(UFnzzyaQT*fr9C~#&y(uTa&M{9-Pf+$Y|FmfhxQLyqxj>l3ZBDbAIe|J'
    '^?%XdAl;VO#1(87S>_uT*REcm1>$ipN>gmYD9K+gce#OgghDk?pJUN*z3L$6kl?LTV=a<p0sHZ8PhUUOD54`%UuJ+Pfjd5f{gF{r'
    '5g60>q4km+^nn28_5fxglP*c;uI}DaPxl{{Tc}f`Tpc@im%4j3&?=L{F0;WY9?Nwo79(HeHb<8Z8Azia?q*1CPAzeVMX#DZI$61O'
    'Xl1Y#v=&k8#(QY|9pc=HxDAkfwqDJ)HQ?*15GK5YLrq5olbn&sD9RZP!s=mA-8LF6YBr9_E)|M>2JrE&7_)c5s$J^pu$|3FVn+Zj'
    '%Y7A)#^5P0kaNF!Zb&U%C}TKJ6GvAcd>h`upgDv<rdt9L(j!*@?D`UTW2+tvddY4*cO5|}5h-3PYnV}SKo4G7efV8ga_C4_&KYGI'
    'Xz~^>TD7qjO={q6qo)_tXk&-70}dyPcs=hY3&(kC5wFAM7Xi0xG>*gK5Zp>7&enIfB@ELiuOnq(DF&kRY`7x7wXvCl7>t%pe^Vrr'
    'f)xRoqNBfg5G6S=I7R#sF^}uR2Wgu*Tq=FVR4B&SiIv&gz^f^8FH#t=<5^>x>NTyNbhY(nsq%4G->1C^W34<hVi)Y|-z_Ja3cK3A'
    'V?eZEM|hEr@}3>#t}bNI270@C%3JpC>r9wGq|D;ZkO|U+J2qOn#YV<kJmV8N6-Y;<j|uZ7x7Sb#*0Vl+<%uRw(-{$8edXK(JZfqb'
    '6{4LT9lN@F${igz3D~{XnOX-$;H?>tI;fQg2UagfoOphGDMIOBnT}_g>htv3Dl5g<)kYD8&8c4x_>JUWfZxbKIywmHkk9(dpY_vj'
    'el5jzB>!jf#?HR#zG{DYPqNi;9=Jh-|LAc1hkSw8=3N4m?R64Eyt}u&6Bh=FL3~Ju;zKe|N410gt}FL>0FKyQ3`LT8=5_^c$bSGQ'
    '!S9IV=rDXq=6<iINdqr`{ydXQ=t%J=eqCf``zqlfs(l<(2yf%Tsm7yYak59msUNZ^FH}=18*%^y6)m=OV{q8IPKNMx+z=#fd8625'
    '_l%%Cvhp65%*r_<3UC}>1i{rBhj^p05ez?%H<}qfn$B+qL3IsOkjm{)>jX3AQX=T)l_MwNMT$F59E;Ci2CpMW+Pm_~U37O_6F!-?'
    '<~cXek~VBE^>pkl4OI2<nOLg&(wbKrl|z?W3-|B9x{dVS!<$54eOgi?_%<~T+Ty6V@)2eY0$^!s%NfqT@C1&oQtq$plTBD1*rQ{U'
    ')3i6y4`o>^<xgdH^&j}&E>WYuqr3N$zK{7jL8m0|4(y<b`}3#h;Lx<(s{d5I*lII?ul6yLT9M&w&YmjL<Trh?e+;5}ZE^Jg*hAfa'
    'F7WIM{8yNhGLhbWNPc(rG*OhaCIdo20kf-Q-0dvz%Ktf8yLQF{M{bQU$1UGd>oa!LsrrcbU=mrm0+QctljodM6OdLPVGFjL2kwg|'
    'P2MZ*Q1WJ!!Zj9eE2dF7Fa9)m7Jm%QFd7-BIShgn3d=K37JD%?FtZRCQeDe&dQdc{@iq^Rg+P!WPX^3L#OBpsz;9e%;CD1r8LMSX'
    'KgDuX%RM6I<w)u4cCu!x@$e>LgItDe>p3<5JD4BF?>oz%?d|E_*&V=nq71=tQ0?y~tZz@BSVQLO?ke^4>?rO0xHEgRO1V_kb2C#v'
    '_K=`U)^DWDd;td(qHw%ij~)B1G>v3;_eZ<!u_9wqKuV8-CZU$9+QIPQgzp*nnNc7y!lStTpEAB>pi#=M8gde_wY9NI8c&wx4e__D'
    'U@OvE1)#g9AEDH0@dPUfeIyZ3BfF0&fGu*UbEFMCF-IQoGYQiaU)A;#r0w>0AvO|ynRdoT+pW-sfVqq<&>08wFz~N63VZGAC(DO{'
    'D!4U>$gy^YL#&QJTf8str-x`BHLVZF)8?G|kNWH*SbMXxDCL-*%GnO~NEQr<jEA>A2sV4M(6~daMU=JCq`%)z1G@cp+uAkDHfq;s'
    'Q8SgeGN#(il_D!;#xl<JQ)KSI(Qc24r&8lXCxagF^5d0-5vyxlJ|3#lv~!1ujYGAS2Pd@sfI`zPUy-*{bZG-s6>DB}mVhfa_6|*e'
    'v|~iPMZ0G;Q)9;NWb7Uf2H|)%bz$Olp<C?`JxF3naHlM=mJv)fyndcAyXj)C=p`)TS*yXxrac<2<7?#xi$;vVc!UzIkWrhwDvwFt'
    '+xlkvyZHaN-^6s>J5V1#M^bU@8~Jl?88m)B95vOXKD>J@2;4E+8B<m19*sAsfrNB}8Z1d8Tio0*y+%hYx*@J&W`~-bPz=g}#^6D_'
    '?bL+T(9cgHE1?_M$=mR_lVfXV&qGTDns)<VomtG0n?%n|nSN=S@L95YtKV8o5`mjI@1PlEO}@o6eAy#rnIVlt12w?jjyf+u4ICds'
    '-{$P`)&8g#I4`7|v$_OTl4KQvwWB`>6MD%%F}T8mZ&&AUWTN(SL#rn*rY!=fU=a*fiDi3wbjg})9Umh{6d|GQJUpSRbl~PVYGg6F'
    'ZiUEalPj_|4#PM!UZ8~#OTAD~b<}vRopuW9IPjYLx{}cWK%twN+l7^5c!u4wv@-t5y%RE(o*OL0eE62kC9;gjDyHmFK0K-ob(TTY'
    '4B{U00NopG{BXCqeUp>Tgxk&=fIw4urr4NpNz=cXJgP2K<MG$4wHu61(HweGn@Ik9LK^hs4`w3id2~2ON>j|NgWFDqt9pDwOv<bp'
    'R#*Ez`VsLRYNN96IDH6J&EH_|Z|$o`P96iA63uCXn0EO4Y`<P@b>KUO!_IV^(((J)w9B)#LD9#_gTe~ycd0;hXTb2iY+$QdQ*uwR'
    '8L^{tqg!+}-zvzN;ZbT|6E#}&$kS;7r}3LBH}d43Vc_xE*ALRI$t~pFJ!EGV<jQLk^$WM-l?MDGynsg%aO2hwvKLtyJ(lVgZ+6Yu'
    '_QT9U`#k8ML3t=SAS`j$CwfdkUxv77L^;R7TkJ;{GBcmKxx%)&4~8|2aAcg>w=fp50R23Bst3~{+rW@~)|!CcVWs?$*u#~5%7)VP'
    '(dNWkJyZXBmYvnMW>k)z;wG*S9vAlt=wR#6Iw3Exwjdk$F<bSqbG5uQ^NZ+w2l}xYY#LoIyHNCpCUoPJt$VB@t6@2)TkUlzj-QRP'
    'cX#i?=}=45ls1FSieiC82&+gf!mMVH&|`-F86FLRcVEPUwgxd&!w|o7S9}&i4<;L8Xl5~+9}^p65Mh>LeIJg{<#ibbOGB@Mtwb4S'
    '0t`-_8vLnDR>2bwm0D0KcNx9sb0XH<pzGCtkn>Z!7=Nz*A9GT(F>jlaG-*jgM3NVS5a1og?wUexlthiVDcKB2Y%A7>!w)!b%XMkb'
    '#2E9f*@lDlg|D$h%!=tgG~fHHs6kHbOQon$OAYID3~cb_q!|6|ck2Y?EC!4L9`*5s6hfq*SSmooodQ2%^5qQp17%86Um}SfPdpMu'
    'x+|<(L?~^dH<iJcJ9I?T5g3y3^2fAhw&3cIV+o0bzxy5V6A#YWf)UqIObnaBACsS{uys@nMh5A%RpgI(gJo|&>omWl9yC}HYEx$v'
    'G>Nmae)lM3UP67aY5mYED!|!8Brzf&T(4tPT);!!K~L0J`v^7;Hc$NE95<u)1U9Mz<$H7kDXhupoy4-)R^p?8G;6nD$7Qwvo&#%F'
    'lU)1}d2fosRV3DJ+uc{~Z{p$E&Mz4jd(FNywqFb6dp{hSi%Q`R#62T+EI@5s{-!aA7(l3=ziNdz8k@69Fp^M_+cr?1Wl3(u*!v25'
    'SviN5==nY*N1Xg30jI_MCeDiwW&NzI5|-kLb+wwdst+q24rN8i0C}@7Mg;?#`VHQZfu}HbYpH<X(xan|%Oi@hGw_X__RkX!EwfA+'
    'k&QrZh5VD^mI@OY+QC&T7XZ8O)RJ5U8l-H?3Thy}f?xwcjaUP$KefH#8v}~NU<ay}58a4iMO@VuAz1)+b!iwFHwx&qvNH*Q4hL}j'
    '6QC&n+#W%3?%(m_7=Q~ARI53t-na{*V9hd}0!JQ!I7~&SVQiEdUS*koE2M*wm6NPr?Go&|$7jWvTsIz1L9eJ`1(Bewv=|lVJ*!LQ'
    '0u)^BBJ+dTKMF?nEVtF(>i|Q+kaqdfUEeySFH<EB_u>1Ze`(yn%2A#X`&Nkqur*aIn6_b01#c*#6uUsBUtonPOwtx{dX;AYIuf#w'
    'qV@Ed8rJ%HWqHvdf7-HO%xitIrD|y7wQ~AfZ(eMQRa2FjwJ!Ddmv-*fyi;~}TWECJno`#<oPsI|K|$(&`wj0(egh!rYAjsT7Da}{'
    'W9bybGDiD$;)>cux<%&FtY-V$d&|9Bx3x1=&1M@gkklX-ja^@Sz>|zh9PPJ3o$tLKEpq=(r4Jc8c&qwzdaLXwm9Gi{FqW_?$J?}}'
    'x%f6lc2#Lc6yipWFt_YP2w>z7sUnAV@#)sbN@W+6tV$k8KiyeQTw_9W?67D+kQb^@a*_-<6<HV%OkfoTjlr?{&>?7K)#LhVZB5uP'
    'HQ4WpInyN5v_30)?qdtqny-h)`m6hMGH^32h0bvPG0>7EA=GepPD;QaJvkb}G{d9jdb4wv66vsq#M-qpp7`NEh>3@l#fFa$Kt&mo'
    '*Vd1Xip87dI8Gr$YL9&dSh!_EhZr1Vk3fW1x4rk<gwaH`ds3>wENLu@$CPdi+&lLVtD06q^?2u4%b|b|18q=1nwehHi&H!|Gj3rn'
    'H&x0#x+6tK_3z%y^ua_ik-aH{-@W(iwqIEV4BjC4hN7&VV1v!B96i2TJ4;T%K-fn1y-ung=5jWL1l9|Q1#@K#;O^E@P#2j!O@v?2'
    'OI~|o8~DbQp=J=NAV}dmlkF*B?B($0PE@cdrY!is`B2lvgUE+^?LkO=JJ*!DLlZ{MrVvb07evU>U&`aEC7IUraaGe>FC^v9_A;ks'
    '3d$b-SkCLYbqZREbB-L`e+Ul=>TS~=OPRlr!15D8%oSDbY8tEwPL<HlU~vl5_Re@xA01h}R1188T2L)lbOgn=36<6_Bz+1;t=dCB'
    '<3qLS&~;iSuI=(mM~M}pGmW7m^$Xud0`<S>Goi~iiCL>#C6Gddt;+k#1HA0cBA5CAQ*(&r1sf%`F%Z2F&yp-cS^D@OIC#(q0wvXt'
    ')isVA4O0jUyRXrByu=fy#X;!W2W+<<W0d(s)oVFwAO~0zgYKSWOzE1T8FmU*Uo9PSi~btqY|9nO<GkSviV!H5svf}Wgztc)Jd~a$'
    'F2$}ZS1ex|kVPzW7X@4D)-acG4yib(4?YO@Dt}hqsi3nCBw9UGO-(jCf+MP&9dU;oPi$tBzsgk-KB6rf>VJ=qdEbUyGms1tw><?r'
    'is5t(#GS_R5r>U(lZUpm&8;x95Yo_|!pbOFDw`n*3$h{ZLAY9*&SPW3E+DU5a2+!&NZyGMHl6A<FC=c&^Q4_Qy0m>JiH+#Cv)75`'
    '-DjC>z?T00%eA@hp42X|4H?OZ_%bQ~l^RyG;pu8+7?=r#gdEXYgwU#KoI^93plUle5fOb@<1IUTN&{VG#g4!E+B?`>ma0>KkvTyY'
    'G#e!|9dTvMGOP%FYuOXZ3l(Y%%*L=Hd0wSDLM(2Jzh->Up{?(1!|W4^r`8b?_P^UE`uWQM91L`3M1;rCbn8(Q`VN!ZDRSv$v+6bt'
    'x5?qM(Mm5us4(F6WO+<s@Po`7vdq)$p2@2$%Awm}metvV^(#a61e*p6J=D*CU4O7lEE9>;DUf|P($sM>$=JO&nKv?ROkf%(C<sS1'
    'MUyoS;U!I(5Qo>~XG`fZSdDf#c)PK1LWHssP-rT3ekuhvMBQ;3ix)8~S?hrn74E>A9jA?bZRsLhi|uCMooK7TOA~2vZ7~ovS$&*U'
    'GJT$nnSGwb1+Z|qX*Fp*tC_W*TOT>f`KF*j{{QJzM|cNo@yd`D{ycIdhERBac0@49+oMU}TyqlSyIb_wEF{pvgU&4GUO(;~Ph#W{'
    'cgVmc44wg3CnSY1ahlD|VKeCCTXj8IIN?EKiS1gphSBy6RE71Mqf7u}*oy1?W7&IB^;O7`RLK3M;T%NYLRSF5UFwTtR_twol!CcP'
    'dlH5I+CCE)lQ@>QYP9&BjLN_rNu1Kb^GS%4ZAk6i)R?%1EY{4tbAd;o)IeH>*k;?#eLKlER!C{)OSsMpSuMK;c5Up2YR%zItj`%c'
    ';pp2S<Qo-U1W;_+(kp_<+BgbF>0`;=--i>yXk%;(W^;a7Tg(}ixemedYk_ezg@`CR+joEFw%LbpkAN~@ZjjA_L?5&>d_a7~dhZe='
    'b5ev!VsoZr!k-F4iK|8!=WiUkO+ICw0r@vKy=7YlFQ>Q_f0a1)m1C>(si%5voWEu~tX|{E)^AbKu#G&z702WSNkH{rdrlpT`A$^e'
    'D1o|0>5(HLdBMVc&R2PFsZwq$CeLd@kP?O;*NIy`eDVk>dy*82lroXIfTkg1)^H^8n1y~?^I=ya&488c4GP3i?&2U3ve&gd)VZs5'
    'OJy%@?kO35>RUN`L5uyLyFBdJ?6oa+T<%(5w){)BT>1D0TshJth{JmC9(*1R`{tJ~&g2(-6R%T#;hHba;2jD+3$q#4q2Oa4uJ|w~'
    'oe@MJ-~A{&0F4yd({<pl_4Z*Rjk**RTw>_zjKtjq@u3!tp^j(iwiw_M4odl7rk3mXE}5j)Xe>}Awc5&;23)3j6%Yj3tg2yxwKYKw'
    '2T-3xdp}0i)kp(A#{5`Kj>91*pq<$?qF@++78}?}M>V&QPy9a^tY1EAf$@+P&hx|}ZgHy?_<v&g#a7YGBu-6OtUyc1GMPPN4Pd48'
    '0d70TUy5q8tfCWlF%Q*};Xp?bQB9IlM<19m7-3<fALTfC8)D82Z2H5>Y<}-3GoD`6?9uahrlWkLo6!d6B1u!!uTBHZphL4@JMk!Y'
    'zOP@HH}FM4J~C~ZlC}6f;^iDp<n?SKhSp|5O>EH_(rZ268lSrL1ZJV}Kc@uP*RR)yuX*rQK{4~#nd9e_&T9~PIK{&kFmU@on<=;h'
    'c!W)cbyDaD5dE)~nlpOFra4(=RxTZ=kNnVV9q0NHTbnJll;aLe-'
    'Ur0nmKka_B5-kI(JgNBgpwqSyp=Hxs+Zf?t<+F~f2t`MLj+hc+z@%B@el2XftxGF!}|uhKK(q&$hF{&IQvnvsZY|QR>Vk-'
    'OO6?m91W}}V#PG;t3>&>Bo{u(M$Lw}qlqwNm7h&Rlr_-YWLm25|Amq_=G(n*y$9xE#_5$CW*!H=GQe+?A_AUX2-4)qy&MQ+-zr^>'
    '2|BqyaWuMy?B5YlscNO{^3^(tr<^e+Kst=sYkIWtc!}H+iqJu9P3<r+OjvA8`wXhdG(5_mW_Vf6*EeXFlB}@4C}m-7F6RxPJ7XDS'
    'G(wRzxoR>7V?gC$zIPsqGQ@U%xn6$E?xb%Uwf3;ay6gir^)(y3Gq4;0G|Th#2i7kVv4r#2n6NP4%d`;quROp@J!mabvytKym19*b'
    'fvCoXn49g+1MIjZ!lJ338@Dg%qASW~@T{CpbhH<@%SB@Wy??v~3R&w51DDNI_KM(`4TOy$mTMgUj5DiGoHEk8Vs@(gSP<k0?{n1l'
    'iXXeddOjTZ%STiC{R{)rlSh}I)P}7kb4w<9?-gt8?pBEy0-Cx!%7o9B0JtD-{uhO<z0*K=XM`0zXMu1MbE?YNgXDh`4@0J7NfQe^'
    'i`K|5K~k9v1^k$I{{-@M?y9l=>pTIGsGLTyRBj%YeT}Tn;)v(3!`6r!Cxq0h2DNt%R4SU9AL!kJY>LywMu5;XHDFb5T5u)C(A*U('
    '1`D7+)+&V}Ls$Q8)e<OR6;`FpZgIl`N<$b;4Cv@1v*dMFbv4l2>$QvN5G=dbWHvPor?BR6CWbm9n4p#$>>Ca=OS$O$Md9Xw#u8~K'
    'S1V3}7O0J5Mj}#T#`O$6<qU5%A3(;Vj2#;{g2|MeSZlNZ3{?z26Ub>q(ZbEOiF6T@fL>j1!JGzl-usINrmuAtF<w5aekVh8$Pp&u'
    '){$prWZXftx%DMkSeA{73V&*%Y3RiTt(tOePJ$SK1BG~U@jzfOu729xzjL=eAGsiA6RdMDRr>XX!hs^`TK<gT6KCelqG(fOKzt6t'
    ')UgYNhp74!+0Vmy;=ZcWbaw<U0<wZ+XEBq;i||E(yPDApO_QYwGZsQNb9DO_$k20#>}$q2$y+0I{j?veD*AJ7TA-)a(uvSDOeK<e'
    'HaZi`H8qJN*8N(l(nwPZ^O)8ck6aYmGD2fvkl+ENgLlrMZQ8TYmG%f>lELbzxdChqg{t$vyALJ*&M*<-g~N_@E0gstaTXGVK$Ay8'
    'UlZgVWp=VRBD7iz7jedyCwL1JcafAc>~93tGYCn@c(lrL7%@dYdkR2=5^?M9*Nxa;8PE*@Wv7c}84Lgm6`c<EH0u2#&yP5v<yE|s'
    '33MxiqEiYq)3g?mB&%a{D?c1fdlTyJiOrjoS2bmP&nmF6c{?b-wgheFdQ5qWodI6^UMO*K=!D?%WT0BEguWi@^od6pAY#B@(@+Y@'
    '?lUCS+Ou6nB=K$ASTz**Jy#AtMwWnFsE6;@A4N=Kbd-_y)0CWw0h$af$B77~=9gC&1{*gYt(<z8>zTx4EKCsvCz|nn`*QuzF*k1t'
    'Y(;~c%uHr!;0|=cH}0#HDmW_(S75Oz7cEbzY?-x_kHOxmey_1ug>0xPj10{c+77wxDE3`!${`7>j-%s39oLC~l|nmHn_N6OE&orG'
    '?_|*l*o}u@d1ljgnqqk4%Dc5or$o%7^*lQ>t8rsqpDZ+eo(SNRX=kD^2K|ZW6v&v7X$?pZTFg78;T#GLXU)X0w<HoYa3p6s_i0H='
    'm};G5(j-&A7-a3Z1<yhwbhn7_n4K_s3SRH{d2^Syy&ml1(Iqk+vWJ|WMQ!lTPT*us4^5&h0jCAoDbsukTo&I(^udjqxLPYGmm-c<'
    'MIKVn;-dmC$aGP}!ik9>Gk$Hn66wCBiL$P3`0|!iRR^amL#2<TX||hKz{55n3*9>iMh9EQxo_gthTO-t;ObAN#NrCKg7e8nDOx)c'
    'HtA{Y->TC~4=M820`+lJwqKt;SH!Lf?pN~j#8C?dI`u5cmI{FmhVYswguHlQ?>@xw{;qwXT4~?W-P>O7{Ujmp(91H8=kUM3w~LaN'
    'R8vV1lZp7xiO1g5^Gm{`s#|zFyxy)#-=3tSW7k0cK&9N#k#z4-N}N)!$Z<9Ms`yp-wDQs3Ql(n<4-m=oKPu_x)qPdFH2{x2-8;g4'
    '-;qD<8<FFp7{V7@d-vf%AM|eB{@%7<Z+-2(j`#lX&KvJ0uO|CH=uPB*K6oydK>gsk7s5A1iRS3*f-GU?+pg?ynF@$L%cS$7&kJ%X'
    '`t29dTfF<;*7tt*uD!@}+S5aR<2MC*mw$&)5C4k$f$)>G!OaKH$pc=0<F#$?ZhQN!+yg>Pspy`e_fCuZz4OLfufOruYk%nYw{35|'
    'Znq1X-7oRhF8PbH!|;wRywEzT@Q={SgMWqgDtf=|Z)|;i+goqEYj=rzAn@1b9)|ZRXsHl=rMHxE{j1M@*g)aYzH9ga_Xq#YU6I=m'
    'zHI!ruQ{jUz2De#v|=l~d0Jh?Pmo^Z%l<C6cwx8Eos9NbOMf!nqp=TFbZxUk7yT-o*Z4@Os}y$h_P2lc-Wwh7zWuxHuZ8&xL@QzT'
    'BF-0kdyu}^Ya8c_zWQr#zxC^F*f(z!-3XK|{FgWvwl9AD#&-O?<K3-qzVq8R3g<1hM57mRd$ed$V3lSMzuKmm%VYc0kN5AlZu{-l'
    'f02>M8JAO-8A$Mgqr1eOaP%=f+~@;(t{KtOU)k3@!shjtHxc=^t2EG)_BxFR;77Mj7QbBS#tBXrTXwQ+j{NWxk1D@z3u24!{?0qw'
    '-~Rp9-~M^8PSMmBfX2<=1Vt9#>f`Rc>zx7mU2TT<j&C_2YP8b*$8uYxyZUj{i)1#nZ3(%mzg*fAwszVrOE={2-`K9g_nocpz4ylU'
    'x8%x7c?*t=xW=30^8e?954wK-!3SILpa0{7=QfKRdhIuFyt%c*E|d#Cc<!YwFF^-ydkeRaVf<bM`oC=Z?Y8&+pkHUZJK~qAQqci-'
    'sN=b=hI|FHQjDv3s`|vW1(1rT>D5=7zeo`kd9{llJBFfI3*#p(P!p|H^egE{dAP*W^f$-f5-brm&l|tp_OII@0N#4zzpIx2*QXdO'
    'uV8bnt8#*B`QoVyv;S!qwpf|{&#Kq*tF`~xuZtFP|5Jb7w2VKI)-HHdi^bocoz7rEbiB8H>s#+`YZ7?T;Qq`7-q5;+Z~uR@l=j<#'
    '@7JAk{I6)Uq{9^Yrah&-@D^T4y2Ms|zuI4U0UpDLFC@FV%RODySCVR3KbB8E-7We-KIpG(PTKxC=@ENGzj=jEE>|id#bgcN|BxT-'
    '>Z|ZBNq28jm3>$4+QhqVwoC9o-Mj2ZVo%_QVL<6G(wlFQ@5;S$mk*xXucP_mmBdV7yUFwW^}k;{uet$OilQH=^x6%g34r~vXK(+$'
    '4tWK+ev|l(1M+tA^7?i=`ucia$=pHIX>V1VLh`pSa3<gm;>Y}O3+}de)8@_TliVToGAH?~q)&?Hl&Z;PY2g&hT<I*OBf+g+(6x)_'
    ')JFV%JWrKAxpL1ywLjTWmO&?dJN~_l2YrZf@*-<C<)_e;f2O&zOL=~owouM}5jCc4^-V_8@H=fVoPR{&gBdW*e;9Su%Y7AB%jLfE'
    'M!ij+r#*LbUuE0q=Dy4{$jyD%s>5XNXGLuQ{U=}K&YPcQZicXqwz)R@*5L)s+ax_2_Cws~{pN-LF!{SzldQQF-'
    'K_Nq&ECiN;Pd;l_lnMJI)-?E>GKPWx(y8?y-_rPf}2I7$jGlv`7Iso*32et-'
    'iA%PFDL$Rq?^Q%zP~>G!_6C(g)aTlgD!XPZPPm^ymLIs#ZQgi=0nOij4)@JZ-MP89?#C*1s82>MdJM+&Os8j(oA{>_Ml=9YakCP*'
    'cdq}@?lUP*(leB1~SkKCr<TKlDVzSr#|iZ%%{(P80`@`sOfphEW7U3LdW9$7O(JO%h6`nKXbOCXZmpC4>8)LtFK(u3DN~i;PT{dx'
    'Z(FsuWVY9Ad;KcB19FQc{zB?^u_eg%mSp3Z!KDVKpqBr=7F@n`Q}J)7AWWUGx|lWDCqQ!9T2}RR@<dYrL-^Fe${exFG#FzQCXj*g'
    '~oX8vYsX}L5fC{moDO>?dtwa)Ji&(E&luW_v`Jy_%OeP3SQo>X|_&Uzd4@`qpsOb+1z{s4~i6tM^T)(ZK@o7Jp0oLNJV+y-Vj?;J'
    'I(~T?dUIFuxZ)6x#_jjUty|!+95W>A4O-QDa`zA6BO@Fe`b1G!h?%WOf>fJyDcA;`!}V7+?>4nY9`3tdTi^?a@1r!4x5L*>g&vF@'
    '%S?L+w4*%7i}sS>{BK)x-WMgIIl(W?SonphY0biM2=QAT2(ycVr5?R9_ch}-u&aooUXoRHZlso={Z4EX;fMU7M886!)@Jta`Jy&>'
    'Zz8WD%4`*_!;F|dg+f7Z9djw;g%WM#^hUi$uo(#Fp&SvLazBLKaj;6D;jz659IWM9K()n-tf)4a7_kqaLz0o(W7?f7cHK}2`Tt8H'
    'ycIrE4+rCERK0|DrB>{w?9Y5`kuoxRK{kW{X9N?ese21n;T~;X`4s-Qz!%<Y@`yh!!^aQ(E;aHeDhPy?PnZvll_?+Zq^cQG3pkwH'
    '}@*OQ#WshP+#A-X?~&B_rq>)8rCz)gdgJ+ZR9|xLQj);W}dX%m9nz?bJ^u6pIq=tPthpvRR+pI`Ghoei=UQy_eHZFNn2#0P0ps@<'
    '2~@j`mfva_c@Cv&7kKL51KE*x+~N${OxAU|3>wsYPX<gi=z!qA*O=JQp-'
    'qDL4+YcG{zR1_4fL@+X4)&)pp?|<lm*>v5|fcKP=SlO}E;Je$QU?3>rRM^S9!}q~qC--M~LwEr$;xNQWuj_1u^AcbIUW{1Epkn2q'
    '=2i)1Yk{(i=}3D^51;U48u@A~^FxBvY#VXTlFg$e+NcXQD(&fjfg)@$Y}4j)TclKbXEZPoSPrugU<oQ>{Y%^}!NWNTY8rCRQP%Dp'
    'xxZ3$DTn==9-d&do*{#av&Cnh$k{n>kdk=g8Vn5-#sCo@-+^v#Vb++G%vMuJa6^TylQ#1uWWWo+(grf7b6PdyIBws0o4mz&sd>n}'
    'v)mi27<8en&~h}q?ZoDJXi<$9Vst3{f^e18X>X%LnT?;$_=o^OiOzkOD9yH)R=HTIq<;<rBsZObCxzW9kpZI5b?pOR!_h4u#P89`'
    '5(_4Lgm&|?<XxbgQ2<5>T_^g-?MGZMF){x>ho#$I6K3)4zSajzFa8)!>E!;43Gz8nxU{wj|g{NhJs2T&uF6bw4qQ^Myvdmu;@(%D'
    '{!`HZKiRp38Z(gF2K|AFa-w?q}YAyJrYD!O`OdN{qrZHQiR+u+h=Vqk&HC3sd2b9)*>!1MMg2WsoYz5nK~o4Nn#^Y-iH$<wa@Kh1'
    'p!V)fiddn<hyXh)w6!4NGQNxKIaWwixvpjwO%=vXIRA1V#=pGPHGfAQ@4f9BTzP&kVGu8K8;7jGFH3vXEkFK|{vv>sXZ<~};VFQi'
    '3$*A|)o+@bL8{x_4zFOJbSAHWZrB~I%><S$>xq0LWNe718(I(L#=AK>h{{TqvDL?bJBVpQ2&eI?Ze5jHLt^6)z8qL4@Ytc^rF{P)'
    'ig=m^*RtpqyK@$640Ak9BSu+@Nncic_DT)LWX8UBcuDZE(tEX2LzM;C-o@;|qMH76$x)Bk5g$io)lGeksWp8Y&OZWS8OjngL0hgs'
    '-xKf~bkeoaHtPd7RuTf1jT>pttu@fpp-)Q_JjsgN6dY*Rkt+3`y^BI#{NJ>}j_@!s7G<&6&!_sD#)aWNQOmM8)uHLC}DyZ>V#GM5'
    'K>Mc$Zf%2-ezw$exP?XxJJ=%(w)mRy1uoG`_vu-9>Q6o5wK0++zlUhspsD|gGU*=M`p>$Hz{!Ivq+yePo_-9@8IJ9D=FcA!%2s}y'
    '_<xe)bO$eZ;6Q=a7`DEJ9h31OoCN~yQn-QQjKqF$cZL2;Cw>o1xXKQH!mqr@xuLaf*_n=KZ`>Dr=TXdvJ$;w8_Xpy+}C4f|b7D9*'
    ';>q0ud$-=F@G6Dkyx@gVbCFFR2FKJQNR)AeNc2H|y?XUn+tzqujDTR%I!nKO}_;P|5a@H1bA`H!n0f7}AZca<fG{`{gTg4iseGA|'
    'WQ@@9hkH$0yB>PVm$i1Ab;Wac!M6xk6+7v$$N`pRZAtpoj0QyJax`Tgl1xoK2!l^K1LD9c3$^@9B9`Du!Sc<(nqZvAr)Cy1bbcW%'
    '%sMY9JmBVENpUw5bI??T=Qv8LXAnqkVG#!R|NFWLAWipG;oFKIN7tq97MV02ZR^DM9g_{`WY;3t2^Fz|vEanVZs#ZRv0a~_xj6WJ'
    'oh`+9o%K2=_V-sF$nd&5NPf8KxTl@E*TCe#M`b@2qpRb%hlGd|ofh%UUMC00zFriv}uZ@2scp}hS*gltZW8n<XGnJUhmU;aG5`#0'
    'tHm;USp85{SPUG`7Oi_u^I{A#qw&%C6(hpy?~K6y^k-v{$5Y}d?r$fXTij1w>NV)9R~rlXDaV5+cWH*&f8vkx~u@T{KFp%cm;4fg'
    '%`^*)Tyjts_S4V<hZYqEaUR~EUS7yHcXD2e=7*7u(aPS&4^{%d-HmKeYo$X5XR3vnIz8vLZYS6&i)1FgV*#pc|Wry8J7#{=@h$PU'
    'bNY~>(7O`cx%23|7If+1l;ZeF3C;vSzZzgvx&0Tg(n_Pu4lTfAQ$sn_Z?^)5|@W%S*TNBC{%t@DV#=+Uhm@pVq2y?%B}ZZ3`nKHK'
    '@+65q3RobZ{4>SyJr8KvEBLj%m6l~*$q9iFk<*c3n0sKZm5_k+RL`xEqX>rb#FqX!keMC|pwx;~pryt(a?zk9WrujD5HZL}OjJY3'
    'I9XP%#x2kYWCH1tb{Jfq2qob;?N3s*%a|COihU%Td6V#B`PO#^Dq*0ziq7ihkXXul0;z-aznX#EROfFRWBh(gT$eequ&@JZ38)s7'
    'q!Pze7R%T1DtDSxrf^IPc34MbvEsK^!{rQ5=`QT*1;$^7+x`?PrSv+kBH>FqHNv)Ee|-(*LYuGQ1tA^zo0{s!UZ8yJ7_xpOPEaD#'
    '1bjlX>6p%y-(B}DUC1|J=PpK`!{+l>x#V^dnNMdBA|4QXjn;F7shHl<W>K%aSH(piYatm)I-!=fA|1GE|SQ4qde*Auc|<-#GBpbG'
    'Y56QdX60`?pE-tO@XnTq@%Lx7~3&H5C-LEdP{(YvugU>!F%c;Ly4uM{T}tCk$et#-93wiHciw&r2|+J}4zLz;3VW+Gw=Ya$8zqWK'
    '|lpeh~fk0@aY%Torg7A%ZX0;;r%`TnWN<B8z*cyRvk4a2g)mc0oqn6D`LWg+aUzY?P<u`IGW;MUv%?&zvsz6Bt+4m5t=B<5NlPU@'
    '>4JU7t4tL;~5mZAI^N-2{~Z@>En#`6Mi0`kw-%W_RwKb5tq;+HM(TzD8xt5Sd_7=W^ezLM;hYrcrnXT>lvUHK*lF96z`A6iU2qY-'
    'U=@y(kz_P{(sX9G{nLgtzu*?L;Kd&O*aLe<3Jm+A8Fk%PWT*IJIGjX<eW5s3>ls#~^JJ{kbv@g4nUQ&*YnuyRmd?da(0+u6~vIlg'
    'pDsjEw7iPyl4mLchZ?UfI!vKxMmKQNY8b*u4Od?ydJTj+mqlj^3;F@&rb|AAwNr|7YhYqsbkUul&fYu@ym*Fm1)=K7(+N%G$kHX^'
    'f!WBbPT?Qd_#4#cmr@l9K!mu#ZK6@e<ZE7`AkXdnFMFVbbxVOBS9$sgtyxsae9=>UYPqk~eoqXX;O(cvywbsyfn8!h3n-g1}czW8'
    '79qr>('
)
# END EMBEDDED CONTRACT BUNDLE


@lru_cache(maxsize=1)
def _resource_files() -> dict[str, str]:
    if not RESOURCE_BUNDLE_B85:
        raise RuntimeError("Embedded product Skill contract bundle is empty")
    try:
        packed = base64.b85decode(RESOURCE_BUNDLE_B85.encode("ascii"))
        value = json.loads(zlib.decompress(packed).decode("utf-8"))
    except (ValueError, UnicodeDecodeError, json.JSONDecodeError, zlib.error) as exc:
        raise RuntimeError(f"Embedded product Skill contract bundle is invalid: {exc}") from exc
    if not isinstance(value, dict) or any(
        not isinstance(key, str) or not isinstance(content, str)
        for key, content in value.items()
    ):
        raise RuntimeError("Embedded product Skill contract bundle must be a text mapping")
    return value


def _safe_read(relative: str) -> str:
    normalized = Path(relative).as_posix().lstrip("/")
    if normalized.startswith("../") or "/../" in normalized or normalized in {".", ".."}:
        raise ValueError(f"Resource path escapes the embedded Skill root: {relative}")
    try:
        return _resource_files()[normalized]
    except KeyError as exc:
        raise FileNotFoundError(f"Embedded Skill resource not found: {normalized}") from exc


def _digest(paths: list[str]) -> str:
    digest = hashlib.sha256()
    for relative in paths:
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(_safe_read(relative).encode("utf-8"))
        digest.update(b"\0")
    return digest.hexdigest()


def validate_product_execution_plan(value: dict[str, Any]) -> dict[str, Any]:
    """Validate and normalize a single-stage, partial-chain, or full-chain plan."""

    if not isinstance(value, dict):
        return {"valid": False, "errors": ["execution plan must be an object"]}
    requested = value.get("stage_chain") or value.get("requested_stages") or []
    if isinstance(requested, str):
        requested = [requested]
    if not isinstance(requested, list):
        return {"valid": False, "errors": ["stage_chain must be an array"]}
    stages = [str(item or "").strip().lower() for item in requested]
    errors: list[str] = []
    if not stages:
        errors.append("stage_chain must contain at least one stage")
    unknown = [stage for stage in stages if stage not in STAGE_SKILLS]
    if unknown:
        errors.append("unsupported stages: " + ", ".join(unknown))
    if len(stages) != len(set(stages)):
        errors.append("stage_chain must not contain duplicate stages")
    known = [stage for stage in stages if stage in STAGE_SKILLS]
    positions = [STAGE_ORDER.index(stage) for stage in known]
    if positions != sorted(positions):
        errors.append("stage_chain must follow the canonical forward stage order")
    if errors:
        return {"valid": False, "errors": errors}
    mode = "full" if tuple(stages) == STAGE_ORDER else ("single" if len(stages) == 1 else "chain")
    normalized = dict(value)
    normalized.update({
        "selected_stage": stages[0],
        "stage_chain": stages,
        "execution_mode": mode,
        "stage_cursor": 0,
    })
    skip_flags = {
        f"skip_{stage}": f"Stage {stage} is outside the approved execution plan."
        for stage in STAGE_ORDER if stage not in stages
    }
    return {
        "valid": True,
        "errors": [],
        "execution_plan": normalized,
        "skip_flags": skip_flags,
        **skip_flags,
    }


def _parse_stage_chain(raw: str) -> list[str]:
    text = str(raw or "").strip()
    if not text:
        return []
    lowered = text.lower()
    if any(marker in lowered for marker in ("all", "full-pipeline", "全流程", "七阶段")):
        return list(STAGE_ORDER)
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        parsed = None
    if isinstance(parsed, dict):
        parsed = parsed.get("stage_chain") or parsed.get("stages")
    if isinstance(parsed, list):
        values = [str(item or "").strip().lower() for item in parsed]
    else:
        values = [
            item.strip().lower()
            for item in re.split(r"\s*(?:->|→|,|，|、|\||/|;|；)\s*", text)
            if item.strip()
        ]
    stages: list[str] = []
    for value in values:
        stage = next((key for key, aliases in STAGE_ALIASES.items() if value in aliases), "")
        if not stage:
            raise ValueError(f"Unsupported product stage: {value}")
        stages.append(stage)
    return stages


def normalize_product_parameters(
    product_goal: str,
    stage_chain: str,
    execution_depth: str,
    word_target: str,
    reference_sample_choice: str,
) -> dict[str, Any]:
    """Normalize mandatory chat preflight answers into deterministic runtime gates."""

    goal = str(product_goal or "").strip()
    if not goal:
        raise ValueError("product_goal is required before starting the Workflow")
    stages = _parse_stage_chain(stage_chain)
    plan_result = validate_product_execution_plan({"stage_chain": stages})
    if not plan_result["valid"]:
        raise ValueError("; ".join(plan_result["errors"]))

    depth_raw = str(execution_depth or "").strip().lower()
    depth_aliases = {
        "light": "light", "轻量": "light", "轻量模式": "light", "精简": "light",
        "复用现有": "light",
        "minimum-fill": "minimum-fill", "最小补齐": "minimum-fill",
        "最小补齐模式": "minimum-fill", "补齐": "minimum-fill",
        "full": "full", "完整": "full", "完整模式": "full", "完整执行": "full",
    }
    depth = depth_aliases.get(depth_raw)
    if not depth:
        raise ValueError("execution_depth must be light, minimum-fill, or full")

    has_text_stage = bool(set(stages) & TEXT_STAGES)
    target_text = str(word_target or "").replace(",", "").strip().lower()
    target: int | None = None
    if has_text_stage:
        match = re.search(r"\d+", target_text)
        if not match:
            raise ValueError("word_target is required when the plan contains a text stage")
        target = int(match.group())
        if target < 300:
            raise ValueError("word_target must be at least 300 Chinese characters")
    elif target_text not in {"", "not-applicable", "n/a", "不适用"}:
        match = re.search(r"\d+", target_text)
        target = int(match.group()) if match else None

    sample_raw = str(reference_sample_choice or "").strip().lower()
    sample_aliases = {
        "provided": "provided", "已提供": "provided", "有样例": "provided",
        "none-confirmed": "none-confirmed", "无样例": "none-confirmed",
        "默认结构": "none-confirmed", "使用默认结构": "none-confirmed",
        "不使用参考样例": "none-confirmed", "不提供参考样例": "none-confirmed",
        "不使用样例": "none-confirmed",
        "not-required": "not-required", "不需要": "not-required", "不适用": "not-required",
    }
    sample_status = sample_aliases.get(sample_raw)
    if has_text_stage and sample_status not in {"provided", "none-confirmed"}:
        raise ValueError(
            "reference_sample_choice must be provided or none-confirmed for text stages"
        )
    if not has_text_stage:
        sample_status = sample_status or "not-required"

    execution_plan = dict(plan_result["execution_plan"])
    execution_plan.update({
        "product_goal": goal,
        "execution_depth": depth,
        "word_target": target,
        "reference_sample_status": sample_status,
    })
    return {
        "execution_plan": execution_plan,
        "skip_flags": plan_result["skip_flags"],
        **plan_result["skip_flags"],
    }


def load_product_skill_contract(
    stage_id: str,
) -> dict[str, Any]:
    """Load one compact packaged router or stage contract without calling a model."""

    raw_stage = str(stage_id or "").strip().lower().replace("_", "-")
    normalized = next((
        key for key, aliases in STAGE_ALIASES.items() if raw_stage in aliases
    ), raw_stage)
    if normalized not in {"router", *STAGE_SKILLS}:
        raise ValueError(f"Unsupported stage_id: {stage_id!r}")

    resources = _resource_files()
    skill_name = "product-solution-delivery"
    if normalized == "router":
        paths = ["SKILL.md", *ROUTER_REFERENCES]
    else:
        skill_name = STAGE_SKILLS[normalized]
        child_prefix = f"children/{skill_name}"
        paths = [f"{child_prefix}/SKILL.md"]
        references = sorted(
            path
            for path in resources
            if path.startswith(f"{child_prefix}/references/") and path.endswith(".md")
        )
        for path in references:
            relative_parts = Path(path).parts
            if "source-snapshots" in relative_parts:
                continue
            paths.append(path)

    unique_paths: list[str] = []
    seen: set[str] = set()
    for path in paths:
        if path not in seen:
            seen.add(path)
            unique_paths.append(path)

    sections = []
    for path in unique_paths:
        sections.append(f"\n\n--- BEGIN {path} ---\n{_safe_read(path)}\n--- END {path} ---")

    return {
        "package_release": PACKAGE_RELEASE,
        "stage_id": normalized,
        "skill_name": skill_name,
        "contract_sha256": _digest(unique_paths),
        "resource_root": (
            f"embedded://product-solution-delivery/children/{skill_name}"
            if normalized != "router"
            else "embedded://product-solution-delivery"
        ),
        "contract_text": "".join(sections).lstrip(),
    }


PRODUCT_STAGE_INPUT_SLOTS = {
    "competitive": (
        "execution_plan", "research_evidence", "material_digest", "direction_document",
    ),
    "prototype": ("execution_plan", "material_digest", "design_document", "prd_document"),
    "delivery": (
        "execution_plan", "direction_document", "competitive_analysis", "design_document",
        "prd_document", "prototype", "review_document", "handoff_document",
    ),
}


def _bound_artifact_payload(value: Any) -> Any:
    current = value
    for _ in range(5):
        if isinstance(current, dict):
            nested = next((
                current[key] for key in ("data", "text") if key in current
            ), current)
            if nested is current:
                return current
            current = nested
            continue
        if isinstance(current, list):
            return current
        text = str(current or "").strip()
        if not text:
            return ""
        try:
            candidate = Path(text).expanduser().resolve()
            if candidate.is_file():
                current = candidate.read_text(encoding="utf-8")
                continue
        except OSError:
            pass
        try:
            current = json.loads(text)
        except json.JSONDecodeError:
            return text
    return current


def _bound_artifact_descriptor(value: Any) -> dict[str, Any]:
    """Describe a delivery input without loading its potentially large document body."""
    current = value
    descriptor: dict[str, Any] = {"present": True}
    for _ in range(6):
        if isinstance(current, dict):
            for key in ("filename", "content_type", "seq", "revision", "version"):
                if current.get(key) not in (None, ""):
                    descriptor[key] = current[key]
            if current.get("path"):
                current = current["path"]
                continue
            nested = next((
                current[key] for key in ("value", "data", "text") if key in current
            ), None)
            if nested is None:
                descriptor.setdefault("content_type", "json")
                descriptor["size_bytes"] = len(json.dumps(current, ensure_ascii=False))
                return descriptor
            current = nested
            continue
        if isinstance(current, list):
            descriptor.setdefault("content_type", "list")
            descriptor["item_count"] = len(current)
            return descriptor
        text = str(current or "").strip()
        try:
            candidate = Path(text).expanduser().resolve()
            if candidate.is_file():
                descriptor.setdefault("filename", candidate.name)
                descriptor.setdefault("content_type", candidate.suffix.lstrip(".") or "file")
                descriptor["size_bytes"] = candidate.stat().st_size
                return descriptor
        except OSError:
            pass
        descriptor.setdefault("content_type", "text")
        descriptor["size_bytes"] = len(text.encode("utf-8"))
        return descriptor
    descriptor.setdefault("content_type", "unknown")
    return descriptor


def load_product_stage_inputs(stage_id: str) -> dict[str, Any]:
    """Read only the immutable materials bound to one non-Writer product stage."""
    raw = str(stage_id or "").strip().lower().replace("_", "-")
    normalized = next((
        key for key, aliases in STAGE_ALIASES.items() if raw in aliases
    ), raw)
    if normalized not in PRODUCT_STAGE_INPUT_SLOTS:
        raise ValueError(
            "stage_id must be competitive, prototype, or delivery for bound material loading"
        )
    context = require_context()
    remote = (context.params or {}).get("remote_inputs") or {}
    if not isinstance(remote, dict):
        remote = {}
    materials = {
        slot: (
            _bound_artifact_descriptor(remote[slot])
            if normalized == "delivery" and slot != "execution_plan"
            else _bound_artifact_payload(remote[slot])
        )
        for slot in PRODUCT_STAGE_INPUT_SLOTS[normalized]
        if remote.get(slot) not in (None, "", [])
    }
    return {
        "stage_id": normalized,
        "materials": materials,
        "available_slots": list(materials),
        "missing_optional_slots": [
            slot for slot in PRODUCT_STAGE_INPUT_SLOTS[normalized] if slot not in materials
        ],
    }


class _PrototypeParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.has_title = False
        self.has_viewport = False
        self.has_h1 = False
        self.interactive_controls = 0
        self.images_without_alt: list[int] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        if tag == "title":
            self.has_title = True
        elif tag == "meta" and (values.get("name") or "").lower() == "viewport":
            self.has_viewport = True
        elif tag == "h1":
            self.has_h1 = True
        elif tag in {"button", "input", "select", "textarea"}:
            self.interactive_controls += 1
        elif tag == "a" and values.get("href"):
            self.interactive_controls += 1
        elif tag == "img" and not (values.get("alt") or "").strip():
            self.images_without_alt.append(self.getpos()[0])


def _validate_prototype(content: str) -> list[str]:
    parser = _PrototypeParser()
    parser.feed(content)
    errors: list[str] = []
    if not parser.has_title:
        errors.append("missing <title>")
    if not parser.has_viewport:
        errors.append("missing viewport meta")
    if not parser.has_h1 and not re.search(r"<h1\b", content, flags=re.I):
        errors.append("missing <h1>")
    if parser.interactive_controls == 0:
        errors.append("prototype has no interactive controls or navigable links")
    if parser.images_without_alt:
        errors.append("images missing alt at lines: " + ", ".join(map(str, parser.images_without_alt)))
    if re.search(r"__+[A-Z0-9_]+__+|\bTODO\b|【[^】]+】", content):
        errors.append("prototype contains unresolved placeholders")
    return errors


def _validate_competitive_report(content: str) -> list[str]:
    errors: list[str] = []
    lowered = content.lower()
    for marker in ("<!doctype html", "<title", 'name="viewport"', "<table", "<svg", "<details"):
        if marker not in lowered:
            errors.append(f"competitive report missing {marker}")
    semantic_sections = {
        "竞品对比": ("我与竞品", "竞品对比", "竞争对比", "能力对比", "对比矩阵"),
        "生态位": ("生态位", "生态地图", "生态定位"),
        "定位": ("定位", "差异化", "位置图"),
        "产品启示": ("产品方案", "产品启示", "方案启示", "产品策略", "设计启示"),
    }
    for label, alternatives in semantic_sections.items():
        if not any(marker in content for marker in alternatives):
            errors.append(f"competitive report missing semantic section: {label}")
    # Source links are evidence-dependent. Requiring arbitrary href values when retrieval
    # returned no sources encourages the model to invent URLs merely to satisfy validation.
    if re.search(r"__+[A-Z0-9_]+__+|\bTODO\b|【[^】]+】", content):
        errors.append("competitive report contains unresolved placeholders")
    return errors


def write_product_artifact(
    filename: str,
    content: str,
    validate_as: str = "none",
) -> dict[str, Any]:
    """Write one generated file and optionally run an embedded HTML validator."""

    safe_name = Path(str(filename or "")).name
    if not safe_name or safe_name in {".", ".."}:
        raise ValueError("filename must contain a safe file name")
    if not isinstance(content, str) or not content.strip():
        raise ValueError("content must be a non-empty string")
    if validate_as not in {"none", "prototype", "competitive"}:
        raise ValueError("validate_as must be 'none', 'prototype', or 'competitive'")

    context = require_context()
    if not context.workspace_path:
        raise RuntimeError("The active Workflow workspace is unavailable")
    output_root = Path(context.workspace_path) / "product-solution-delivery" / "artifacts"
    output_root.mkdir(parents=True, exist_ok=True)
    output_path = output_root / f"{uuid.uuid4().hex}-{safe_name}"
    output_path.write_text(content, encoding="utf-8")

    validation = {"valid": True, "output": "validation not requested"}
    if validate_as == "prototype":
        errors = _validate_prototype(content)
        validation = {
            "valid": not errors,
            "output": "PASS" if not errors else "FAIL: " + "; ".join(errors),
        }
    elif validate_as == "competitive":
        errors = _validate_competitive_report(content)
        validation = {
            "valid": not errors,
            "output": "PASS" if not errors else "FAIL: " + "; ".join(errors),
        }

    return {
        "path": str(output_path.resolve()),
        "sha256": hashlib.sha256(output_path.read_bytes()).hexdigest(),
        "validation": validation,
    }


def write_product_artifact_file(
    filename: str,
    content: str,
    validate_as: str = "none",
) -> str:
    """Write a product HTML artifact and return only its exact saveable file path.

    Lightweight validation remains diagnostic and non-blocking. Human approval is the quality
    gate; a semantic wording variation must not force full-document regeneration or hide the file.
    """
    result = write_product_artifact(filename, content, validate_as)
    return str(result["path"])
